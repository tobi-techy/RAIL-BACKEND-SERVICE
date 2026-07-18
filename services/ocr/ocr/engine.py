"""PaddleOCR engine wrapper with PDF rasterization.

PaddleOCR is heavy to construct, so a single instance is lazily created and
reused across requests. Input bytes (image or PDF) are turned into a list of
page images, each preprocessed and OCR'd, then flattened into line results.
"""

from __future__ import annotations

import io
from dataclasses import dataclass, field

import cv2
import fitz  # PyMuPDF
import numpy as np
from PIL import Image

from preprocess import preprocess

# Rasterization DPI for PDF pages. 200 balances legibility vs. memory.
PDF_DPI = 200
# Hard cap to protect the worker from pathological multi-hundred-page PDFs.
MAX_PAGES = 30

_engine = None


def _get_engine():
    global _engine
    if _engine is None:
        from paddleocr import PaddleOCR

        # angle classifier handles rotated lines; English model also reads
        # the Latin-script + digits that dominate NGN/GBP/USD statements.
        _engine = PaddleOCR(use_angle_cls=True, lang="en", show_log=False)
    return _engine


@dataclass
class Line:
    text: str
    bbox: list[list[float]]
    confidence: float


@dataclass
class OCRPage:
    page: int
    lines: list[Line] = field(default_factory=list)


def _pdf_to_images(data: bytes) -> list[np.ndarray]:
    images: list[np.ndarray] = []
    with fitz.open(stream=data, filetype="pdf") as doc:
        zoom = PDF_DPI / 72.0
        matrix = fitz.Matrix(zoom, zoom)
        for page in doc:
            if len(images) >= MAX_PAGES:
                break
            pix = page.get_pixmap(matrix=matrix, alpha=False)
            arr = np.frombuffer(pix.samples, dtype=np.uint8).reshape(pix.h, pix.w, pix.n)
            if pix.n == 4:
                arr = cv2.cvtColor(arr, cv2.COLOR_RGBA2BGR)
            elif pix.n == 3:
                arr = cv2.cvtColor(arr, cv2.COLOR_RGB2BGR)
            images.append(arr)
    return images


def _image_bytes_to_array(data: bytes) -> list[np.ndarray]:
    img = Image.open(io.BytesIO(data)).convert("RGB")
    arr = cv2.cvtColor(np.array(img), cv2.COLOR_RGB2BGR)
    return [arr]


def _to_page_images(data: bytes, mime_type: str) -> list[np.ndarray]:
    if mime_type == "application/pdf" or data[:5] == b"%PDF-":
        return _pdf_to_images(data)
    return _image_bytes_to_array(data)


def _run_paddle(img: np.ndarray) -> list[Line]:
    engine = _get_engine()
    result = engine.ocr(img, cls=True)
    lines: list[Line] = []
    # PaddleOCR returns [[ [bbox, (text, conf)], ... ]] (one entry per image).
    if not result or result[0] is None:
        return lines
    for entry in result[0]:
        try:
            bbox, (text, conf) = entry[0], entry[1]
        except (ValueError, TypeError):
            continue
        if not text:
            continue
        lines.append(Line(text=text, bbox=bbox, confidence=float(conf)))
    return lines


def recognize(data: bytes, mime_type: str) -> tuple[list[OCRPage], str, float]:
    """OCR the document. Returns (pages, full_text, mean_confidence)."""
    page_images = _to_page_images(data, mime_type)
    pages: list[OCRPage] = []
    text_parts: list[str] = []
    confidences: list[float] = []

    for idx, raw in enumerate(page_images):
        processed = preprocess(raw)
        lines = _run_paddle(processed)
        pages.append(OCRPage(page=idx + 1, lines=lines))
        for ln in lines:
            text_parts.append(ln.text)
            confidences.append(ln.confidence)
        if idx < len(page_images) - 1:
            text_parts.append("---PAGE BREAK---")

    full_text = "\n".join(text_parts).strip()
    mean_conf = sum(confidences) / len(confidences) if confidences else 0.0
    return pages, full_text, mean_conf
