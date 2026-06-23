"""
Transaction Enrichment API — FastAPI sidecar service.
Wraps the ML brand classifier + industry mapper behind a simple HTTP endpoint.
"""

import warnings
from pathlib import Path

import joblib
from fastapi import FastAPI
from pydantic import BaseModel

from src.brand_matcher import clean_merchant_name
from src.industry_classifier import classify_industry

app = FastAPI(title="Transaction Enrichment Service")

MODEL_PATH = Path("models/brand_classifier.joblib")
MIN_CONFIDENCE = 0.25

_model = None


def get_model():
    global _model
    if _model is None:
        if MODEL_PATH.exists():
            _model = joblib.load(MODEL_PATH)
        else:
            warnings.warn(f"Model not found at {MODEL_PATH}")
    return _model


class EnrichRequest(BaseModel):
    raw_description: str
    mcc_code: int | None = None


class EnrichResponse(BaseModel):
    counterparty: str
    category_l1: str | None
    category_l2: str | None
    is_essential: bool
    confidence: float
    classification_layer: str  # "ml" or "rule"


class BatchEnrichRequest(BaseModel):
    transactions: list[EnrichRequest]


class BatchEnrichResponse(BaseModel):
    results: list[EnrichResponse]


def enrich_single(req: EnrichRequest) -> EnrichResponse:
    cleaned = clean_merchant_name(req.raw_description)
    model = get_model()

    brand = None
    confidence = 0.0
    layer = "rule"

    if model is not None:
        proba = model.predict_proba([cleaned])[0]
        top_idx = proba.argmax()
        brand = model.classes_[top_idx]
        confidence = float(proba[top_idx])
        layer = "ml"

        if confidence < MIN_CONFIDENCE or brand == "Other":
            brand = None
            confidence = 0.0

    # Industry classification
    l1, l2, essential = classify_industry(brand, req.mcc_code)

    # Fallback counterparty: use cleaned name if no brand detected
    counterparty = brand if brand else cleaned.title()

    return EnrichResponse(
        counterparty=counterparty,
        category_l1=l1,
        category_l2=l2,
        is_essential=essential,
        confidence=confidence if confidence > 0 else 0.1,
        classification_layer=layer if brand else "rule",
    )


@app.post("/enrich", response_model=EnrichResponse)
def enrich(req: EnrichRequest):
    return enrich_single(req)


@app.post("/enrich/batch", response_model=BatchEnrichResponse)
def enrich_batch(req: BatchEnrichRequest):
    return BatchEnrichResponse(results=[enrich_single(t) for t in req.transactions])


@app.get("/health")
def health():
    model = get_model()
    return {"status": "ok", "model_loaded": model is not None}
