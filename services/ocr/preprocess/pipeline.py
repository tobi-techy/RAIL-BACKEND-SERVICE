"""OpenCV preprocessing pipeline to boost OCR accuracy on scanned documents.

Order matters: grayscale -> denoise -> shadow removal -> contrast -> deskew.
Each step is defensive: any failure falls back to the previous image so a
malformed page never aborts the whole request.
"""

from __future__ import annotations

import cv2
import numpy as np

# Upper bound on the longest edge after resize. Larger images cost OCR time
# without accuracy gains; smaller scans get upscaled for small-font legibility.
MAX_EDGE = 2000
MIN_EDGE = 1000


def _resize(img: np.ndarray) -> np.ndarray:
    h, w = img.shape[:2]
    longest = max(h, w)
    if longest == 0:
        return img
    scale = 1.0
    if longest > MAX_EDGE:
        scale = MAX_EDGE / longest
    elif longest < MIN_EDGE:
        scale = MIN_EDGE / longest
    if abs(scale - 1.0) < 1e-3:
        return img
    interp = cv2.INTER_AREA if scale < 1.0 else cv2.INTER_CUBIC
    return cv2.resize(img, None, fx=scale, fy=scale, interpolation=interp)


def _to_gray(img: np.ndarray) -> np.ndarray:
    if len(img.shape) == 2:
        return img
    return cv2.cvtColor(img, cv2.COLOR_BGR2GRAY)


def _remove_shadows(gray: np.ndarray) -> np.ndarray:
    """Normalize uneven lighting by dividing out a dilated/blurred background."""
    dilated = cv2.dilate(gray, np.ones((7, 7), np.uint8))
    bg = cv2.medianBlur(dilated, 21)
    diff = 255 - cv2.absdiff(gray, bg)
    return cv2.normalize(diff, None, 0, 255, cv2.NORM_MINMAX, dtype=cv2.CV_8U)


def _denoise(gray: np.ndarray) -> np.ndarray:
    return cv2.fastNlMeansDenoising(gray, None, h=10, templateWindowSize=7, searchWindowSize=21)


def _contrast(gray: np.ndarray) -> np.ndarray:
    clahe = cv2.createCLAHE(clipLimit=2.0, tileGridSize=(8, 8))
    return clahe.apply(gray)


def _deskew(gray: np.ndarray) -> np.ndarray:
    """Estimate page skew from dark-pixel coordinates and rotate to correct it."""
    inverted = cv2.bitwise_not(gray)
    thresh = cv2.threshold(inverted, 0, 255, cv2.THRESH_BINARY | cv2.THRESH_OTSU)[1]
    coords = np.column_stack(np.where(thresh > 0))
    if coords.shape[0] < 50:
        return gray
    angle = cv2.minAreaRect(coords)[-1]
    if angle < -45:
        angle = 90 + angle
    # Only correct meaningful skew; tiny angles add rotation noise.
    if abs(angle) < 0.5 or abs(angle) > 45:
        return gray
    h, w = gray.shape[:2]
    matrix = cv2.getRotationMatrix2D((w / 2, h / 2), angle, 1.0)
    return cv2.warpAffine(
        gray, matrix, (w, h), flags=cv2.INTER_CUBIC, borderMode=cv2.BORDER_REPLICATE
    )


def _safe(step, img: np.ndarray) -> np.ndarray:
    try:
        out = step(img)
        return out if out is not None else img
    except Exception:
        return img


def preprocess(img: np.ndarray) -> np.ndarray:
    """Run the full enhancement pipeline. Returns a single-channel uint8 image."""
    img = _safe(_resize, img)
    gray = _safe(_to_gray, img)
    gray = _safe(_remove_shadows, gray)
    gray = _safe(_denoise, gray)
    gray = _safe(_contrast, gray)
    gray = _safe(_deskew, gray)
    return gray
