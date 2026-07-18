"""Tests for the OpenCV preprocessing pipeline.

These run without PaddleOCR (no model download) so they stay fast in CI.
"""

import numpy as np

from preprocess import preprocess
from preprocess.pipeline import _deskew, _resize, _to_gray


def _synthetic_doc(h=1200, w=900):
    img = np.full((h, w, 3), 255, dtype=np.uint8)
    # a few dark bars to emulate text rows
    for y in range(100, h - 100, 60):
        img[y : y + 20, 80 : w - 80] = 20
    return img


def test_preprocess_returns_single_channel_uint8():
    out = preprocess(_synthetic_doc())
    assert out.dtype == np.uint8
    assert out.ndim == 2


def test_resize_upscales_small_images():
    small = np.full((300, 200, 3), 255, dtype=np.uint8)
    out = _resize(small)
    assert max(out.shape[:2]) >= 1000


def test_resize_downscales_large_images():
    large = np.full((4000, 3000, 3), 255, dtype=np.uint8)
    out = _resize(large)
    assert max(out.shape[:2]) <= 2000


def test_to_gray_idempotent_on_gray():
    gray = np.full((100, 100), 128, dtype=np.uint8)
    assert _to_gray(gray).ndim == 2


def test_deskew_handles_blank_image():
    blank = np.full((500, 500), 255, dtype=np.uint8)
    # Should not raise and should return same shape.
    out = _deskew(blank)
    assert out.shape == blank.shape


def test_preprocess_never_raises_on_tiny_image():
    tiny = np.full((3, 3, 3), 255, dtype=np.uint8)
    out = preprocess(tiny)
    assert out is not None
