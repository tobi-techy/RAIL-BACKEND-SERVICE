# Rail OCR Service

Python sidecar that turns document bytes (image or PDF) into structured OCR
text using OpenCV preprocessing + PaddleOCR. It performs OCR only; storage,
classification, field extraction, validation, and LLM categorization all live
in the Go backend (`internal/domain/services/document`).

## Endpoints

- `GET /health` -> `{"status":"ok"}`
- `POST /ocr` with `{file_b64, mime_type, doc_hint}` ->
  `{text, page_count, mean_confidence, lines:[{text,bbox,confidence,page}]}`

## Local run

```bash
pip install -r requirements.txt
uvicorn app:app --host 0.0.0.0 --port 8091
```

## Tests

```bash
pip install pytest
pytest tests/ -v   # preprocessing tests (no model download)
```
