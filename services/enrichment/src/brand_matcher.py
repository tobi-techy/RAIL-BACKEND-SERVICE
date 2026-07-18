"""
Transaction Enrichment Pipeline — Stage 1: Feature Extraction.

Cleans raw transaction descriptions and extracts features for downstream stages.
Each stage is independently testable.
"""

import re
from typing import Optional


def clean_merchant_name(name: str) -> str:
    """Clean raw merchant/transaction description for classification."""
    if not isinstance(name, str):
        return ""
    name = name.lower()
    # Strip payment processor prefixes
    name = re.sub(r'^(sq \*|tst\*|pp\*|zelle |venmo |cashapp\*)', '', name)
    # Strip store numbers
    name = re.sub(r'#\d+', '', name)
    # Strip non-alphanumeric (keep spaces)
    name = re.sub(r'[^a-z0-9\s]', '', name)
    # Collapse whitespace
    name = re.sub(r'\s+', ' ', name)
    # Strip trailing numbers + location codes (store numbers, city/state after brand)
    name = re.sub(r'\s+\d+(\s+[a-z]+)*$', '', name)
    return name.strip()


def extract_features(raw_description: str, mcc_code: Optional[int] = None) -> dict:
    """Extract features from a raw transaction description.

    Returns a dict with:
      - cleaned: cleaned merchant name for ML classification
      - raw: original description
      - mcc_code: merchant category code if available
      - has_amount: whether description contains a numeric amount
      - word_count: number of words in cleaned description
    """
    cleaned = clean_merchant_name(raw_description)
    return {
        "cleaned": cleaned,
        "raw": raw_description,
        "mcc_code": mcc_code,
        "has_amount": bool(re.search(r'\d+[.,]\d{2}', raw_description)),
        "word_count": len(cleaned.split()) if cleaned else 0,
    }
