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

import re

from src.brand_matcher import extract_features, clean_merchant_name
from src.bank_parser import parse_bank_description
from src.industry_classifier import classify_industry, describe_transaction, find_brand_in_text
from src.behavior_tagger import detect_behavior_tags, BehaviorTag
from src.fact_extractor import extract_facts, TransactionFact
from src.embedder import generate_embedding
from src.spend_rules import bucket_for, classify_narration


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
    spend_bucket: str
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

        # A POS/purchase line names the shop, not the bank that cleared it.
        prefer_merchant = bool(re.search(r"\bpos\b|purchase", raw_lower))
        mentioned = find_brand_in_text(raw_description, prefer_merchant=prefer_merchant)

        # Determine best counterparty: ML brand > known brand in the narration
        # > bank parser > cleaned text. Empty parser merchants (STAN terminal
        # ids) are not counterparties.
        parser_merchant = (bank_parsed.cleaned_merchant or "").strip()
        if brand:
            counterparty = brand
            confidence = ml_confidence
            classification_layer = "ml"
        elif mentioned:
            counterparty = mentioned
            confidence = 0.8
            classification_layer = "rule"
        elif bank_parsed.confidence >= 0.7 and parser_merchant:
            counterparty = parser_merchant
            confidence = bank_parsed.confidence
            classification_layer = "bank_parser"
        else:
            counterparty = cleaned.title() if cleaned else raw_description.strip()
            confidence = 0.25
            classification_layer = "rule"

        # Stage 4: Intent Classification
        # Known brand mentioned in the narration beats a bank token the parser
        # picked up on the same line ("POS SHOPRITE ... GTBANK").
        effective_brand = brand or mentioned
        if not effective_brand and bank_parsed.confidence >= 0.7 and parser_merchant:
            effective_brand = parser_merchant
        effective_mcc = mcc_code or None
        l1, l2, essential = classify_industry(effective_brand, effective_mcc)
        plain_desc, merchant_ctx = describe_transaction(effective_brand, effective_mcc, raw_description)

        # Narration rules fill the gap for free-text Nigerian descriptions and
        # correct a high-confidence contradiction (betting labelled as a bank).
        narration = classify_narration(raw_description)
        if narration and (l1 is None or (narration.overrides and l1 == "Financial" and narration.category_l1 != "Financial")):
            l1, l2, essential = narration.category_l1, narration.category_l2, narration.is_essential
            plain_desc, merchant_ctx = narration.plain_description, narration.merchant_context
            if classification_layer != "ml" or narration.overrides:
                confidence = max(confidence, narration.confidence) if l1 else narration.confidence
                if classification_layer == "rule":
                    confidence = narration.confidence
            if (
                not brand
                and not mentioned
                and not parser_merchant
                and narration.counterparty
                and classification_layer == "rule"
            ):
                counterparty = narration.counterparty

        if l1 is None:
            l1, l2, essential = "Uncategorized", "Other", False
            confidence = min(confidence, 0.25)

        spend_bucket = bucket_for(l1, l2)

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

        # Confidence is the classification confidence. Behavior tags and facts
        # are separate signals — counting them used to push unknown narrations
        # toward 0.5 without a real category.
        return EnrichmentResult(
            counterparty=counterparty,
            confidence=round(min(max(confidence, 0.0), 1.0), 4),
            classification_layer=classification_layer,
            category_l1=l1,
            category_l2=l2,
            spend_bucket=spend_bucket,
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



