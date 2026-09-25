"""Deterministic spend classification for bank narrations.

Brand lookup and MCC cover a short list of exact names. Nigerian statements
are mostly free-text narrations (POS, NIP, airtime, bills), so this layer
scans the raw description and returns a category when those lookups miss.

The flat ``bucket`` values match the statement taxonomy stored by the Go
parser (see internal/domain/services/statement/categorize.go) and Miriam's
document categorizer. Keep the three rule sets aligned.
"""

from __future__ import annotations

import re
from dataclasses import dataclass
from typing import Optional


@dataclass(frozen=True)
class SpendMatch:
    bucket: str
    category_l1: str
    category_l2: str
    is_essential: bool
    plain_description: str
    merchant_context: str
    confidence: float
    # High-confidence narrations override a contradictory brand/LLM guess
    # (a GTBank token inside a Shoprite POS line, "BET9JA" labelled shopping).
    overrides: bool = False
    counterparty: str = ""


def _rule(
    pattern: str,
    bucket: str,
    l1: str,
    l2: str,
    essential: bool,
    plain: str,
    context: str,
    confidence: float,
    overrides: bool = False,
    flags: int = re.IGNORECASE,
) -> tuple:
    return (re.compile(pattern, flags), bucket, l1, l2, essential, plain, context, confidence, overrides)


# First match wins. Specific merchants and income/transfer verbs come before
# broad tokens like "bank" or "payment".
_RULES = [
    _rule(
        r"\b(bet9ja|sportybet|betking|1xbet|betway|nairabet|msport|bangbet)\b",
        "betting", "Entertainment", "Betting", False,
        "Betting wager", "Betting platform", 0.95, True,
    ),
    _rule(
        r"\b(salary|payroll|wages?)\b",
        "salary", "Income", "Salary", True,
        "Salary payment", "Employer payroll", 0.9, True,
    ),
    _rule(
        r"\b(sms\s*alert|stamp\s*duty|account\s*maintenance|vat\s+on|commission|card\s+maintenance|transfer\s+charge)\b",
        "fees", "Financial", "Bank Fees", False,
        "Bank fee", "Account charge", 0.9, True,
    ),
    _rule(
        r"\b(atm|cash\s+withdrawal|atm\s*wdl|atm\s+cash)\b",
        "atm", "Financial", "Cash Withdrawal", False,
        "Cash withdrawal", "ATM withdrawal", 0.9, True,
    ),
    _rule(
        r"\b(airtime|data\s+bundle|data\s+recharge|mtn|glo|airtel|9\s*mobile)\b",
        "airtime", "Housing", "Phone", True,
        "Airtime or data", "Telecom top-up", 0.9, True,
    ),
    _rule(
        r"\b(ikedc|aedc|ekedc|ibedc|phed|kedco|eedc|jedc|phcn|nepa|lawma)\b",
        "utilities", "Housing", "Electricity", True,
        "Electricity bill", "Power utility", 0.92, True,
    ),
    _rule(
        r"\b(dstv|gotv|startimes|showmax|netflix|spotify|youtube\s*premium|apple\.com/bill)\b",
        "subscription", "Entertainment", "Streaming", False,
        "Subscription", "Recurring media or TV bill", 0.9, True,
    ),
    _rule(
        r"\b(rent|landlord)\b",
        "rent", "Housing", "Rent", True,
        "Rent payment", "Housing rent", 0.85, True,
    ),
    _rule(
        r"\b(shoprite|spar|justrite|ebeano|game\s+store|prince\s+ebean|supermarket|grocer\w*)\b",
        "groceries", "Food & Drink", "Groceries", True,
        "Groceries", "Supermarket", 0.9, True,
    ),
    _rule(
        r"\b(chicken\s+republic|kfc|mr\s+biggs|dominos?|pizza\s+hut|coldstone|bukka|chowdeck|glovo|restaurant|eatery)\b",
        "food", "Food & Drink", "Fast Food", False,
        "Meal", "Restaurant or food delivery", 0.85, False,
    ),
    _rule(
        r"\b(uber|bolt|indrive|in-?drive|taxify|fuel|filling\s+station|totalenergies|nnpc|oando)\b",
        "transport", "Transport", "Rideshare", False,
        "Transport", "Ride or fuel", 0.85, False,
    ),
    _rule(
        r"\b(pharmacy|healthplus|medplus|hospital|clinic|laboratory|\blab\b)\b",
        "health", "Health", "Pharmacy", True,
        "Health purchase", "Pharmacy or clinic", 0.85, True,
    ),
    _rule(
        r"\b(tuition|school\s+fee|university|waec|jamb|neco)\b",
        "education", "Education", "Education", True,
        "Education payment", "School or exam fee", 0.85, True,
    ),
    _rule(
        r"\b(piggyvest|cowrywise|stash|savings\s+deposit)\b",
        "savings", "Financial", "Savings", True,
        "Savings transfer", "Savings pot", 0.85, True,
    ),
    _rule(
        r"\b(loan\s+repay|loan\s+deduction|loan\s+repayment)\b",
        "loan", "Financial", "Loan", True,
        "Loan repayment", "Debt payment", 0.85, True,
    ),
    # NOTE: transfer_in must come before transfer_out (first match wins).
    # "NIP CREDIT ..." contains "nip" — if transfer_out ran first every
    # incoming transfer would be labelled outgoing.
    _rule(
        r"\b(nip\s+credit|transfer\s+from|received\s+from|inward\s+transfer)\b",
        "transfer_in", "Income", "Transfer In", False,
        "Transfer in", "Money received", 0.8, True,
    ),
    _rule(
        r"\b(nip|trf\s+to|transfer\s+to|sent\s+to|funds?\s+transfer)\b",
        "transfer_out", "Financial", "Bank Transfer", False,
        "Transfer out", "Bank transfer to someone else", 0.8, True,
    ),
]


def classify_narration(raw: str) -> Optional[SpendMatch]:
    """Return the first narration rule that matches, or None."""
    if not raw or not raw.strip():
        return None
    for compiled, bucket, l1, l2, essential, plain, context, confidence, overrides in _RULES:
        match = compiled.search(raw)
        if not match:
            continue
        label = match.group(0).strip()
        pretty = " ".join(label.split()).title()
        return SpendMatch(
            bucket=bucket,
            category_l1=l1,
            category_l2=l2,
            is_essential=essential,
            plain_description=f"{plain} — {pretty}" if pretty else plain,
            merchant_context=context,
            confidence=confidence,
            overrides=overrides,
            counterparty=pretty,
        )
    return None


_L2_BUCKET = {
    "Groceries": "groceries",
    "Fast Food": "food",
    "Coffee": "food",
    "Dining": "food",
    "Restaurant": "food",
    "Desserts": "food",
    "Bakery": "food",
    "Rideshare": "transport",
    "Fuel": "transport",
    "Electricity": "utilities",
    "Utilities": "utilities",
    "Internet": "utilities",
    "Phone": "airtime",
    "Pharmacy": "health",
    "Medical": "health",
    "Gym": "health",
    "Education": "education",
    "Rent": "rent",
    "Streaming": "subscription",
    "Movies": "entertainment",
    "Betting": "betting",
    "Electronics": "shopping",
    "General": "shopping",
    "Online Marketplace": "shopping",
    "Jewelry": "shopping",
    "Music": "shopping",
    "Bank Transfer": "transfer_out",
    "Mobile Money": "transfer_out",
    "Payment Processor": "transfer_out",
    "Bank Fees": "fees",
    "Cash Withdrawal": "atm",
    "Savings": "savings",
    "Loan": "loan",
    "Salary": "salary",
    "Transfer In": "transfer_in",
}


def bucket_for(category_l1: Optional[str], category_l2: Optional[str]) -> str:
    """Map an L1/L2 pair onto the flat statement bucket."""
    if category_l2 and category_l2 in _L2_BUCKET:
        bucket = _L2_BUCKET[category_l2]
        if category_l1 == "Income" and bucket == "transfer_out":
            return "transfer_in"
        return bucket
    if category_l1 == "Income":
        return "salary"
    if category_l1 == "Uncategorized":
        return "other"
    return "other"
