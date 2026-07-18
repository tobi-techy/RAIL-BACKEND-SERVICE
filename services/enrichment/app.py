"""
Transaction Enrichment API — FastAPI sidecar service.
Wraps the ML brand classifier + enrichment pipeline behind a simple HTTP endpoint.
Returns merchant identity, categories, behavior tags, facts, embeddings, and plain-English descriptions.
"""

import warnings
from pathlib import Path
from typing import Optional, List

import joblib
from fastapi import FastAPI
from pydantic import BaseModel

from src.brand_matcher import clean_merchant_name
from src.industry_classifier import classify_industry, describe_transaction
from src.pipeline import EnrichmentPipeline, EnrichmentResult

app = FastAPI(title="Transaction Enrichment Service")

MODEL_PATH = Path("models/brand_classifier.joblib")
MIN_CONFIDENCE = 0.25

_model = None
_pipeline = None


def get_model():
    global _model
    if _model is None:
        if MODEL_PATH.exists():
            _model = joblib.load(MODEL_PATH)
        else:
            warnings.warn(f"Model not found at {MODEL_PATH}")
    return _model


def get_pipeline() -> EnrichmentPipeline:
    global _pipeline
    if _pipeline is None:
        _pipeline = EnrichmentPipeline(model=get_model())
    return _pipeline


class EnrichRequest(BaseModel):
    raw_description: str
    mcc_code: Optional[int] = None
    amount: Optional[float] = None
    historical_amounts: Optional[List[float]] = None
    historical_dates: Optional[List[str]] = None


class EnrichResponse(BaseModel):
    counterparty: str
    category_l1: Optional[str]
    category_l2: Optional[str]
    is_essential: bool
    confidence: float
    classification_layer: str
    plain_description: str
    merchant_context: str
    behavior_tags: List[dict] = []
    facts: List[dict] = []
    embedding: List[float] = []
    bank: Optional[str] = None
    tx_type: Optional[str] = None


class BatchEnrichRequest(BaseModel):
    transactions: List[EnrichRequest]


class BatchEnrichResponse(BaseModel):
    results: List[EnrichResponse]


def _result_to_response(r: EnrichmentResult) -> EnrichResponse:
    return EnrichResponse(
        counterparty=r.counterparty,
        category_l1=r.category_l1,
        category_l2=r.category_l2,
        is_essential=r.is_essential,
        confidence=r.confidence,
        classification_layer=r.classification_layer,
        plain_description=r.plain_description,
        merchant_context=r.merchant_context,
        behavior_tags=r.behavior_tags,
        facts=r.facts,
        embedding=r.embedding,
        bank=r.bank,
        tx_type=r.tx_type,
    )


def enrich_single(req: EnrichRequest) -> EnrichResponse:
    pipeline = get_pipeline()
    result = pipeline.enrich(
        raw_description=req.raw_description,
        mcc_code=req.mcc_code,
        amount=req.amount,
        historical_amounts=req.historical_amounts,
        historical_dates=req.historical_dates,
    )
    return _result_to_response(result)


@app.post("/enrich", response_model=EnrichResponse)
def enrich(req: EnrichRequest):
    return enrich_single(req)


@app.post("/enrich/batch", response_model=BatchEnrichResponse)
def enrich_batch(req: BatchEnrichRequest):
    return BatchEnrichResponse(results=[enrich_single(t) for t in req.transactions])


@app.get("/health")
def health():
    model = get_model()
    pipeline = get_pipeline()
    return {"status": "ok", "model_loaded": model is not None, "pipeline_ready": pipeline is not None}
