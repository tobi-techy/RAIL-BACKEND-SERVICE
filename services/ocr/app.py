"""OCR service — FastAPI sidecar wrapping PaddleOCR + OpenCV preprocessing.

Contract (matches the Go document.OCREngine interface):
  POST /ocr  { "file_b64": "...", "mime_type": "image/jpeg", "doc_hint": "" }
  -> { "text", "page_count", "mean_confidence", "lines": [{text,bbox,confidence,page}] }

The Go worker owns storage, classification, extraction, validation, and LLM
calls. This service does exactly one thing: turn bytes into structured text.
"""

from __future__ import annotations

import base64

from fastapi import FastAPI, HTTPException
from pydantic import BaseModel

from ocr import recognize

app = FastAPI(title="Rail OCR Service")

# 20MB decoded ceiling mirrors the Go upload limit.
MAX_BYTES = 20 * 1024 * 1024


class OCRRequest(BaseModel):
    file_b64: str
    mime_type: str = ""
    doc_hint: str = ""


class OCRLine(BaseModel):
    text: str
    bbox: list[list[float]]
    confidence: float
    page: int


class OCRResponse(BaseModel):
    text: str
    page_count: int
    mean_confidence: float
    lines: list[OCRLine]


@app.get("/health")
def health() -> dict[str, str]:
    return {"status": "ok"}


@app.post("/ocr", response_model=OCRResponse)
def ocr(req: OCRRequest) -> OCRResponse:
    try:
        data = base64.b64decode(req.file_b64, validate=True)
    except Exception:
        raise HTTPException(status_code=400, detail="invalid base64 payload")

    if not data:
        raise HTTPException(status_code=400, detail="empty file")
    if len(data) > MAX_BYTES:
        raise HTTPException(status_code=413, detail="file too large")

    try:
        pages, full_text, mean_conf = recognize(data, req.mime_type)
    except Exception as exc:  # noqa: BLE001 - surface a clean 422 to the caller
        raise HTTPException(status_code=422, detail=f"ocr failed: {exc}")

    lines: list[OCRLine] = []
    for page in pages:
        for ln in page.lines:
            lines.append(
                OCRLine(
                    text=ln.text,
                    bbox=ln.bbox,
                    confidence=ln.confidence,
                    page=page.page,
                )
            )

    return OCRResponse(
        text=full_text,
        page_count=len(pages),
        mean_confidence=round(mean_conf, 4),
        lines=lines,
    )
