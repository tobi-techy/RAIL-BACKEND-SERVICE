"""
industry_classifier.py — Map brands to L1/L2 categories + essentiality.
Ported from d-daemon/transaction-enrichment-ml, extended with essentiality flag.
"""

BRAND_INDUSTRY_MAP: dict[str, tuple[str, str, bool]] = {
    # (L1, L2, is_essential)
    "starbucks": ("Food & Drink", "Coffee", False),
    "mcdonalds": ("Food & Drink", "Fast Food", False),
    "fairprice": ("Food & Drink", "Groceries", True),
    "grab": ("Transport", "Rideshare", False),
    "shell": ("Transport", "Fuel", True),
    "apple store": ("Shopping", "Electronics", False),
    "guardian": ("Health", "Pharmacy", True),
    "netflix": ("Entertainment", "Streaming", False),
    "spotify": ("Entertainment", "Streaming", False),
    "amazon": ("Shopping", "General", False),
    "walmart": ("Food & Drink", "Groceries", True),
    "costco": ("Food & Drink", "Groceries", True),
    "target": ("Shopping", "General", False),
    "uber": ("Transport", "Rideshare", False),
    "lyft": ("Transport", "Rideshare", False),
    "con edison": ("Housing", "Electricity", True),
    "pg&e": ("Housing", "Electricity", True),
    "comcast": ("Housing", "Internet", True),
    "at&t": ("Housing", "Phone", True),
    "verizon": ("Housing", "Phone", True),
    "t-mobile": ("Housing", "Phone", True),
    "state farm": ("Insurance", "Auto", True),
    "geico": ("Insurance", "Auto", True),
    "kaiser": ("Health", "Medical", True),
    "cvs": ("Health", "Pharmacy", True),
    "walgreens": ("Health", "Pharmacy", True),
    "planet fitness": ("Health", "Gym", False),
    "hulu": ("Entertainment", "Streaming", False),
    "disney": ("Entertainment", "Streaming", False),
    "youtube": ("Entertainment", "Streaming", False),
}

MCC_LOOKUP: dict[int, tuple[str, str, bool]] = {
    5814: ("Food & Drink", "Fast Food", False),
    5411: ("Food & Drink", "Groceries", True),
    5541: ("Transport", "Fuel", True),
    5732: ("Shopping", "Electronics", False),
    5912: ("Health", "Pharmacy", True),
    4121: ("Transport", "Rideshare", False),
    4900: ("Housing", "Utilities", True),
    5311: ("Shopping", "General", False),
    5812: ("Food & Drink", "Dining", False),
    7832: ("Entertainment", "Movies", False),
}


def classify_industry(brand: str | None, mcc_code: int | str | None) -> tuple[str | None, str | None, bool]:
    """Return (L1, L2, is_essential). Brand takes precedence over MCC."""
    if isinstance(brand, str):
        b = brand.lower().strip()
        if b != "other" and b in BRAND_INDUSTRY_MAP:
            return BRAND_INDUSTRY_MAP[b]

    try:
        mcc_int = int(mcc_code) if mcc_code else None
        if mcc_int and mcc_int in MCC_LOOKUP:
            return MCC_LOOKUP[mcc_int]
    except (TypeError, ValueError):
        pass

    return (None, None, False)
