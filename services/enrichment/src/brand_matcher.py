"""
brand_matcher.py — Clean raw merchant names for vectorization.
Ported from d-daemon/transaction-enrichment-ml.
"""

import re


def clean_merchant_name(name: str) -> str:
    if not isinstance(name, str):
        return ""
    name = name.lower()
    # Strip payment processor prefixes
    name = re.sub(r'^(sq \*|tst\*|pp\*|zelle |venmo |cashapp\*)', '', name)
    name = re.sub(r'#\d+', '', name)
    name = re.sub(r'[^a-z0-9\s]', '', name)
    name = re.sub(r'\s+', ' ', name)
    return name.strip()
