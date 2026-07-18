"""Tests for the enrichment pipeline stages."""
import sys
from pathlib import Path

# Add parent to path so we can import src modules
sys.path.insert(0, str(Path(__file__).resolve().parent.parent))

from src.brand_matcher import extract_features, clean_merchant_name
from src.bank_parser import parse_bank_description
from src.industry_classifier import classify_industry, describe_transaction
from src.behavior_tagger import detect_behavior_tags, BehaviorTag
from src.fact_extractor import extract_facts
from src.pipeline import EnrichmentPipeline


class TestBrandMatcher:
    def test_clean_merchant_name(self):
        assert clean_merchant_name("SQ *STARBUCKS 1234 SEATTLE WA") == "starbucks"
        assert clean_merchant_name("TST*UBER EATS 0123 LAGOS") == "uber eats"
        assert clean_merchant_name("PP*PAYPAL PURCHASE") == "paypal purchase"

    def test_extract_features(self):
        features = extract_features("SQ *STARBUCKS 1234 SEATTLE WA")
        assert features["cleaned"] == "starbucks"
        assert features["has_amount"] is False
        assert features["word_count"] == 1

    def test_extract_features_with_amount(self):
        features = extract_features("POS PURCHASE #1234 ₦5,000.00")
        assert features["has_amount"] is True


class TestBankParser:
    def test_pos_description(self):
        result = parse_bank_description("POS 012345 STAN001234 LAGOS NG")
        assert result.tx_type == "pos"
        assert result.confidence >= 0.7

    def test_nip_transfer(self):
        result = parse_bank_description("NIP GTB/OBADEJO/0123456789/TRANSFER")
        assert result.tx_type == "nip"
        assert result.bank == "gtbank"
        assert result.confidence >= 0.7

    def test_airtime_purchase(self):
        result = parse_bank_description("AIRTIME PURCHASE MTN 08012345678")
        assert result.tx_type == "airtime"
        assert "mtn" in result.cleaned_merchant.lower()
        assert result.confidence >= 0.8

    def test_bill_payment(self):
        result = parse_bank_description("IKEDC PAYMENT 0123456789")
        assert result.tx_type == "bill"
        assert "ikedc" in result.cleaned_merchant.lower()

    def test_unknown_description(self):
        result = parse_bank_description("SOME RANDOM TEXT 12345")
        assert result.confidence <= 0.5


class TestIndustryClassifier:
    def test_known_brand(self):
        l1, l2, essential = classify_industry("Starbucks", None)
        assert l1 == "Food & Drink"
        assert l2 is not None

    def test_unknown_brand(self):
        l1, l2, essential = classify_industry("SomeRandomShop", None)
        assert l1 is None

    def test_describe_transaction(self):
        plain, ctx = describe_transaction("Starbucks", None, "SQ *STARBUCKS 1234")
        assert "starbucks" in plain.lower()
        assert len(ctx) > 0


class TestBehaviorTagger:
    def test_subscription_detection(self):
        tags = detect_behavior_tags("netflix", "Entertainment", "Streaming")
        assert any(t.tag == "likely_subscription" for t in tags)

    def test_gambling_detection(self):
        tags = detect_behavior_tags("sportybet betting", "Other", "Betting")
        assert any(t.tag == "gambling" for t in tags)

    def test_money_transfer(self):
        tags = detect_behavior_tags("nip transfer to john", "Financial", None)
        assert any(t.tag == "money_transfer" for t in tags)

    def test_unusually_high(self):
        tags = detect_behavior_tags(
            "expensive restaurant", "Food & Drink", "Restaurant",
            amount=50000, historical_amounts=[5000, 6000, 4000],
        )
        assert any(t.tag == "unusually_high" for t in tags)


class TestFactExtractor:
    def test_subscription_fact(self):
        tag = BehaviorTag(tag="likely_subscription", confidence=0.85, metadata={})
        facts = extract_facts("Netflix", "Entertainment", "Streaming", False, [tag])
        assert any(f.fact_type == "recurring_expense" for f in facts)

    def test_essential_expense(self):
        facts = extract_facts("IKEJA ELECTRIC", "Housing", "Electricity", True, [])
        assert any(f.fact_type == "essential_expense" for f in facts)


class TestPipeline:
    def test_enrich_known_brand(self):
        pipeline = EnrichmentPipeline(model=None)
        result = pipeline.enrich("SQ *STARBUCKS 1234 SEATTLE WA")
        assert result.counterparty is not None
        assert result.confidence > 0
        assert result.classification_layer in ("ml", "rule", "bank_parser")
        assert result.plain_description is not None
        assert result.embedding is not None

    def test_enrich_nigerian_bank(self):
        pipeline = EnrichmentPipeline(model=None)
        result = pipeline.enrich("AIRTIME PURCHASE MTN 08012345678")
        assert result.tx_type == "airtime"
        assert result.bank is None  # MTN isn't a bank
        assert result.confidence > 0.5
