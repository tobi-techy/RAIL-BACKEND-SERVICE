"""
industry_classifier.py — Map brands to L1/L2 categories + essentiality + descriptions.
Ported from d-daemon/transaction-enrichment-ml, extended with essentiality flag
and plain-English descriptions for Miriam's transaction understanding.
"""

from typing import Dict, Tuple, Optional, Union

BRAND_INDUSTRY_MAP: Dict[str, Tuple[str, str, bool, str, str]] = {
    # (L1, L2, is_essential, plain_description_template, merchant_context)
    # Global brands
    "starbucks": ("Food & Drink", "Coffee", False, "Coffee at Starbucks", "Coffee chain"),
    "mcdonalds": ("Food & Drink", "Fast Food", False, "Meal at McDonald's", "Fast food restaurant"),
    "fairprice": ("Food & Drink", "Groceries", True, "Groceries at FairPrice", "Supermarket chain"),
    "grab": ("Transport", "Rideshare", False, "Grab ride", "Ride-hailing service"),
    "shell": ("Transport", "Fuel", True, "Fuel at Shell", "Gas station"),
    "apple store": ("Shopping", "Electronics", False, "Purchase at Apple Store", "Electronics retailer"),
    "guardian": ("Health", "Pharmacy", True, "Pharmacy purchase at Guardian", "Pharmacy chain"),
    "netflix": ("Entertainment", "Streaming", False, "Netflix subscription", "Video streaming service"),
    "spotify": ("Entertainment", "Streaming", False, "Spotify subscription", "Music streaming service"),
    "amazon": ("Shopping", "General", False, "Purchase on Amazon", "Online marketplace"),
    "walmart": ("Food & Drink", "Groceries", True, "Groceries at Walmart", "Supermarket chain"),
    "costco": ("Food & Drink", "Groceries", True, "Groceries at Costco", "Warehouse supermarket"),
    "target": ("Shopping", "General", False, "Purchase at Target", "Retail store"),
    "uber": ("Transport", "Rideshare", False, "Uber ride", "Ride-hailing service"),
    "lyft": ("Transport", "Rideshare", False, "Lyft ride", "Ride-hailing service"),
    "con edison": ("Housing", "Electricity", True, "Electricity bill — Con Edison", "Utility company"),
    "pg&e": ("Housing", "Electricity", True, "Electricity bill — PG&E", "Utility company"),
    "comcast": ("Housing", "Internet", True, "Internet bill — Comcast", "Internet provider"),
    "at&t": ("Housing", "Phone", True, "Phone bill — AT&T", "Telecom provider"),
    "verizon": ("Housing", "Phone", True, "Phone bill — Verizon", "Telecom provider"),
    "t-mobile": ("Housing", "Phone", True, "Phone bill — T-Mobile", "Telecom provider"),
    "state farm": ("Insurance", "Auto", True, "Auto insurance — State Farm", "Insurance company"),
    "geico": ("Insurance", "Auto", True, "Auto insurance — GEICO", "Insurance company"),
    "kaiser": ("Health", "Medical", True, "Medical visit — Kaiser", "Healthcare provider"),
    "cvs": ("Health", "Pharmacy", True, "Pharmacy purchase at CVS", "Pharmacy chain"),
    "walgreens": ("Health", "Pharmacy", True, "Pharmacy purchase at Walgreens", "Pharmacy chain"),
    "planet fitness": ("Health", "Gym", False, "Gym membership — Planet Fitness", "Gym chain"),
    "hulu": ("Entertainment", "Streaming", False, "Hulu subscription", "Video streaming service"),
    "disney": ("Entertainment", "Streaming", False, "Disney+ subscription", "Video streaming service"),
    "youtube": ("Entertainment", "Streaming", False, "YouTube subscription", "Video streaming service"),

    # Nigerian brands
    "opay": ("Financial", "Mobile Money", False, "OPay transfer", "Mobile payment platform"),
    "palmpay": ("Financial", "Mobile Money", False, "PalmPay transfer", "Mobile payment platform"),
    "kuda": ("Financial", "Bank Transfer", False, "Kuda bank transfer", "Digital bank"),
    "moniepoint": ("Financial", "Mobile Money", False, "Moniepoint payment", "Mobile payment platform"),
    "gtbank": ("Financial", "Bank Transfer", False, "GTBank transfer", "Commercial bank"),
    "access bank": ("Financial", "Bank Transfer", False, "Access Bank transfer", "Commercial bank"),
    "zenith bank": ("Financial", "Bank Transfer", False, "Zenith Bank transfer", "Commercial bank"),
    "uba": ("Financial", "Bank Transfer", False, "UBA transfer", "Commercial bank"),
    "first bank": ("Financial", "Bank Transfer", False, "First Bank transfer", "Commercial bank"),
    "fidelity bank": ("Financial", "Bank Transfer", False, "Fidelity Bank transfer", "Commercial bank"),
    "stanbic ibtc": ("Financial", "Bank Transfer", False, "Stanbic IBTC transfer", "Commercial bank"),
    "sterling bank": ("Financial", "Bank Transfer", False, "Sterling Bank transfer", "Commercial bank"),
    "paga": ("Financial", "Mobile Money", False, "Paga payment", "Mobile payment platform"),
    "flutterwave": ("Financial", "Payment Processor", False, "Flutterwave payment", "Payment processor"),
    "paystack": ("Financial", "Payment Processor", False, "Paystack payment", "Payment processor"),
    "jumia": ("Shopping", "Online Marketplace", False, "Purchase on Jumia", "Online marketplace"),
    "konga": ("Shopping", "Online Marketplace", False, "Purchase on Konga", "Online marketplace"),
    "bolt": ("Transport", "Rideshare", False, "Bolt ride", "Ride-hailing service"),
    "in-drive": ("Transport", "Rideshare", False, "inDrive ride", "Ride-hailing service"),
    "glo": ("Housing", "Phone", True, "Glo airtime/data", "Telecom provider"),
    "mtn": ("Housing", "Phone", True, "MTN airtime/data", "Telecom provider"),
    "airtel": ("Housing", "Phone", True, "Airtel airtime/data", "Telecom provider"),
    "9mobile": ("Housing", "Phone", True, "9mobile airtime/data", "Telecom provider"),
    "dstv": ("Entertainment", "Streaming", False, "DStv subscription", "Satellite TV provider"),
    "showmax": ("Entertainment", "Streaming", False, "Showmax subscription", "Video streaming service"),
    "bet9ja": ("Entertainment", "Betting", False, "Bet9ja wager", "Betting platform"),
    "sportybet": ("Entertainment", "Betting", False, "SportyBet wager", "Betting platform"),
    "betking": ("Entertainment", "Betting", False, "BetKing wager", "Betting platform"),
    "shoprite": ("Food & Drink", "Groceries", True, "Groceries at Shoprite", "Supermarket chain"),
    "spar": ("Food & Drink", "Groceries", True, "Groceries at SPAR", "Supermarket chain"),
    "mr biggs": ("Food & Drink", "Fast Food", False, "Meal at Mr Biggs", "Fast food restaurant"),
    "tantalizers": ("Food & Drink", "Fast Food", False, "Meal at Tantalizers", "Fast food restaurant"),
    "chicken republic": ("Food & Drink", "Fast Food", False, "Meal at Chicken Republic", "Fast food restaurant"),
    "dominos": ("Food & Drink", "Fast Food", False, "Meal at Domino's", "Pizza delivery"),
    "pizza hut": ("Food & Drink", "Fast Food", False, "Meal at Pizza Hut", "Pizza restaurant"),
    "coldstone": ("Food & Drink", "Desserts", False, "Ice cream at Cold Stone", "Ice cream chain"),
    "bukka hut": ("Food & Drink", "Restaurant", False, "Meal at Bukka Hut", "Local restaurant"),
    "the place": ("Food & Drink", "Restaurant", False, "Meal at The Place", "Fast casual restaurant"),
    "sweet sensation": ("Food & Drink", "Bakery", False, "Pastries from Sweet Sensation", "Bakery chain"),
}

MCC_LOOKUP: Dict[int, Tuple[str, str, bool, str, str]] = {
    # (L1, L2, is_essential, plain_description_template, merchant_context)
    5814: ("Food & Drink", "Fast Food", False, "Fast food purchase", "Fast food restaurant"),
    5411: ("Food & Drink", "Groceries", True, "Grocery purchase", "Supermarket"),
    5541: ("Transport", "Fuel", True, "Fuel purchase", "Gas station"),
    5732: ("Shopping", "Electronics", False, "Electronics purchase", "Electronics store"),
    5912: ("Health", "Pharmacy", True, "Pharmacy purchase", "Pharmacy"),
    4121: ("Transport", "Rideshare", False, "Ride-hailing trip", "Ride-hailing service"),
    4900: ("Housing", "Utilities", True, "Utility payment", "Utility company"),
    5311: ("Shopping", "General", False, "Retail purchase", "Retail store"),
    5812: ("Food & Drink", "Dining", False, "Restaurant dining", "Restaurant"),
    7832: ("Entertainment", "Movies", False, "Movie tickets", "Cinema"),
    5733: ("Shopping", "Music", False, "Music purchase", "Music store"),
    5944: ("Shopping", "Jewelry", False, "Jewelry purchase", "Jewelry store"),
    8011: ("Health", "Medical", True, "Medical service", "Healthcare provider"),
    8299: ("Education", "Education", True, "Education payment", "Educational institution"),
}


def classify_industry(brand: Optional[str], mcc_code: Optional[Union[int, str]]) -> Tuple[Optional[str], Optional[str], bool]:
    """Return (L1, L2, is_essential). Brand takes precedence over MCC."""
    if isinstance(brand, str):
        b = brand.lower().strip()
        if b != "other" and b in BRAND_INDUSTRY_MAP:
            return BRAND_INDUSTRY_MAP[b][:3]

    try:
        mcc_int = int(mcc_code) if mcc_code else None
        if mcc_int and mcc_int in MCC_LOOKUP:
            return MCC_LOOKUP[mcc_int][:3]
    except (TypeError, ValueError):
        pass

    return (None, None, False)


def describe_transaction(brand: Optional[str], mcc_code: Optional[Union[int, str]], raw_description: str = "") -> Tuple[str, str]:
    """Return (plain_description, merchant_context) for a transaction.

    Uses brand/MCC lookup when available, falls back to a cleaned version of the raw description.
    """
    if isinstance(brand, str):
        b = brand.lower().strip()
        if b != "other" and b in BRAND_INDUSTRY_MAP:
            entry = BRAND_INDUSTRY_MAP[b]
            return (entry[3], entry[4])

    try:
        mcc_int = int(mcc_code) if mcc_code else None
        if mcc_int and mcc_int in MCC_LOOKUP:
            entry = MCC_LOOKUP[mcc_int]
            return (entry[3], entry[4])
    except (TypeError, ValueError):
        pass

    # Fallback: use a cleaned version of the raw description
    cleaned = raw_description.strip().title() if raw_description else "Unknown transaction"
    return (cleaned, "")
