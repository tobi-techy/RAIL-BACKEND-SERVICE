"""
Transaction Enrichment Pipeline — Orchestrator.

Chains all pipeline stages into a single enrichment flow.
Each stage is independently testable; this module only handles orchestration.

Pipeline:
  Input Event
  → Feature Extraction (brand_matcher)
  → Bank Description Parsing (bank_parser)
  → Entity Resolution (brand_classifier — ML model)
  → Intent Classification (industry_classifier)
  → Behavior Detection (behavior_tagger)
  → Fact Extraction (fact_extractor)
  → Embedding Generation (embedder)
  → Confidence Scoring
  → Output Enrichment
"""

from dataclasses import dataclass, field
from typing import Optional, List

from src.brand_matcher import extract_features, clean_merchant_name
from src.bank_parser import parse_bank_description
from src.industry_classifier import classify_industry, describe_transaction
from src.behavior_tagger import detect_behavior_tags, BehaviorTag
from src.fact_extractor import extract_facts, TransactionFact
from src.embedder import generate_embedding


@dataclass
class EnrichmentResult:
    """Complete output of the enrichment pipeline."""
    # Entity resolution
    counterparty: str
    confidence: float
    classification_layer: str  # "ml", "rule", "bank_parser", "llm"

    # Intent classification
    category_l1: Optional[str]
    category_l2: Optional[str]
    is_essential: bool
    plain_description: str
    merchant_context: str

    # Behavior tags
    behavior_tags: List[dict] = field(default_factory=list)

    # Financial facts
    facts: List[dict] = field(default_factory=list)

    # Embedding
    embedding: List[float] = field(default_factory=list)

    # Metadata
    bank: Optional[str] = None
    tx_type: Optional[str] = None
    raw_description: str = ""


class EnrichmentPipeline:
    """Orchestrates all enrichment stages into a single flow."""

    def __init__(self, model=None):
        """Initialize the pipeline with an optional pre-loaded ML model."""
        self._model = model

    def enrich(
        self,
        raw_description: str,
        mcc_code: Optional[int] = None,
        amount: Optional[float] = None,
        historical_amounts: Optional[List[float]] = None,
        historical_dates: Optional[List[str]] = None,
    ) -> EnrichmentResult:
        """Run the full enrichment pipeline on a single transaction."""
        raw_lower = raw_description.lower()

        # Stage 1: Feature Extraction
        features = extract_features(raw_description, mcc_code)

        # Stage 2: Bank Description Parsing (Nigerian bank formats)
        bank_parsed = parse_bank_description(raw_description)

        # Stage 3: Entity Resolution (ML brand classification)
        brand = None
        ml_confidence = 0.0
        layer = "rule"

        cleaned = features["cleaned"]
        if self._model is not None and cleaned:
            try:
                proba = self._model.predict_proba([cleaned])[0]
                top_idx = proba.argmax()
                brand = self._model.classes_[top_idx]
                ml_confidence = float(proba[top_idx])
                layer = "ml"
                if ml_confidence < 0.25 or brand == "Other":
                    brand = None
                    ml_confidence = 0.0
                    layer = "rule"
            except Exception:
                pass

        # Determine best counterparty: ML brand > bank parser > raw
        if brand:
            counterparty = brand
            confidence = ml_confidence
            classification_layer = "ml"
        elif bank_parsed.confidence >= 0.7:
            counterparty = bank_parsed.cleaned_merchant
            confidence = bank_parsed.confidence
            classification_layer = "bank_parser"
        else:
            counterparty = cleaned.title() if cleaned else raw_description.strip()
            confidence = 0.3
            classification_layer = "rule"

        # Stage 4: Intent Classification
        effective_brand = brand or (bank_parsed.cleaned_merchant if bank_parsed.confidence >= 0.7 else None)
        effective_mcc = mcc_code or None
        l1, l2, essential = classify_industry(effective_brand, effective_mcc)
        plain_desc, merchant_ctx = describe_transaction(effective_brand, effective_mcc, raw_description)

        # Stage 5: Behavior Detection
        behavior_tags = detect_behavior_tags(
            cleaned_description=cleaned,
            category_l1=l1,
            category_l2=l2,
            amount=amount,
            historical_amounts=historical_amounts,
            historical_dates=historical_dates,
        )

        # Stage 6: Fact Extraction
        facts = extract_facts(
            counterparty=counterparty,
            category_l1=l1,
            category_l2=l2,
            is_essential=essential,
            behavior_tags=behavior_tags,
            amount=amount,
        )

        # Stage 7: Embedding Generation
        embedding_text = f"{counterparty} {l1 or ''} {l2 or ''} {plain_desc}"
        embedding = generate_embedding(embedding_text.strip())

        # Stage 8: Confidence Scoring (composite)
        composite_confidence = _compute_composite_confidence(
            ml_confidence=ml_confidence,
            bank_confidence=bank_parsed.confidence,
            behavior_count=len(behavior_tags),
            fact_count=len(facts),
        )

        return EnrichmentResult(
            counterparty=counterparty,
            confidence=composite_confidence,
            classification_layer=classification_layer,
            category_l1=l1,
            category_l2=l2,
            is_essential=essential,
            plain_description=plain_desc,
            merchant_context=merchant_ctx,
            behavior_tags=[{"tag": t.tag, "confidence": t.confidence, "metadata": t.metadata} for t in behavior_tags],
            facts=[{"type": f.fact_type, "value": f.value, "confidence": f.confidence, "category": f.category} for f in facts],
            embedding=embedding,
            bank=bank_parsed.bank,
            tx_type=bank_parsed.tx_type,
            raw_description=raw_description,
        )


def _compute_composite_confidence(
    ml_confidence: float,
    bank_confidence: float,
    behavior_count: int,
    fact_count: int,
) -> float:
    """Compute a composite confidence score from all pipeline signals."""
    base = max(ml_confidence, bank_confidence, 0.3)

    # Boost for behavioral signals
    behavior_boost = min(behavior_count * 0.05, 0.15)

    # Boost for extracted facts
    fact_boost = min(fact_count * 0.03, 0.10)

    return min(base + behavior_boost + fact_boost, 1.0)
