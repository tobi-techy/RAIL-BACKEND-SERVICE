"""
Transaction Enrichment Pipeline — Bank Description Parser.

Parses raw bank transaction descriptions from Nigerian and global banks.
Handles POS, NIP, USSD, transfer, and card transaction formats.
"""

import re
from dataclasses import dataclass
from typing import Optional


@dataclass
class ParsedBankDescription:
    """Result of parsing a raw bank transaction description."""
    bank: Optional[str]
    tx_type: Optional[str]  # pos, nip, ussd, transfer, card, airtime, etc.
    reference: Optional[str]
    location: Optional[str]
    cleaned_merchant: str
    confidence: float


# Nigerian bank description patterns
POS_PATTERNS = [
    # "POS 012345 STAN001234 LAGOS NG"
    re.compile(r'pos\s+(\d+)\s+(stan\w*)\s+(\w+(?:\s+\w+)?)\s+(\w{2})', re.IGNORECASE),
    # "POS PURCHASE MERCHANT NAME LAGOS"
    re.compile(r'pos\s+purchase\s+(.+?)(?:\s+([A-Z]{2,}))?$', re.IGNORECASE),
]

NIP_PATTERNS = [
    # "NIP GTB/OBADEJO/0123456789/TRANSFER"
    re.compile(r'nip\s+(\w+)/(\w+)/(\d+)/(\w+)', re.IGNORECASE),
    # "NIP TRANSFER FROM JOHN DOE"
    re.compile(r'nip\s+transfer\s+(?:from|to)\s+(.+)', re.IGNORECASE),
]

USSD_PATTERNS = [
    # "USSD Transfer to +2348012345678"
    re.compile(r'ussd\s+transfer\s+(?:to|from)\s+(.+)', re.IGNORECASE),
]

TRANSFER_PATTERNS = [
    # "Transfer from GTBank - 0123456789"
    re.compile(r'transfer\s+(?:from|to)\s+(\w+)(?:\s*-\s*(\d+))?', re.IGNORECASE),
    # "GTB NIP CREDIT 0123456789 JOHN DOE"
    re.compile(r'(\w{3,4})\s+nip\s+credit\s+(\d+)\s+(.+)', re.IGNORECASE),
]

AIRTIME_PATTERNS = [
    # "AIRTIME PURCHASE MTN 08012345678"
    re.compile(r'airtime\s+purchase\s+(\w+)\s*(\d*)', re.IGNORECASE),
    # "MTN AIRTIME 500"
    re.compile(r'(\w+)\s+airtime\s+(\d+)', re.IGNORECASE),
]

BILL_PATTERNS = [
    # "IKEDC PAYMENT 0123456789"
    re.compile(r'(ikedc|aedc|ibedc|kedco|phed|eedc)\s+payment\s+(\d+)', re.IGNORECASE),
    # "DSTV SUBSCRIPTION 0123456789"
    re.compile(r'(dstv|gotv|startimes)\s+subscription\s+(\d+)', re.IGNORECASE),
]

# Global patterns
GLOBAL_PATTERNS = [
    # "SQ *STARBUCKS 1234 SEATTLE WA"
    re.compile(r'sq\s*\*(.+?)(?:\s+\d+)?(?:\s+[A-Z]{2})?$', re.IGNORECASE),
    # "TST*UBER EATS 0123 LAGOS"
    re.compile(r'tst\*(.+?)(?:\s+\d+)?(?:\s+\w+)?$', re.IGNORECASE),
    # "PP*PAYPAL PURCHASE"
    re.compile(r'pp\*(.+)', re.IGNORECASE),
]

BANK_KEYWORDS = {
    "gtb": "gtbank", "gtbank": "gtbank",
    "access": "access bank", "accessbank": "access bank",
    "zenith": "zenith bank",
    "uba": "uba", "united bank": "uba",
    "first bank": "first bank", "firstbank": "first bank",
    "fidelity": "fidelity bank",
    "stanbic": "stanbic ibtc",
    "sterling": "sterling bank",
    "kuda": "kuda",
    "opay": "opay",
    "palmpay": "palmpay",
    "moniepoint": "moniepoint",
    "paga": "paga",
    "wema": "wema bank",
    "union": "union bank",
}


def parse_bank_description(raw: str) -> ParsedBankDescription:
    """Parse a raw bank transaction description into structured fields."""
    if not raw:
        return ParsedBankDescription(None, None, None, None, raw, 0.0)

    raw_upper = raw.upper().strip()

    # Try POS patterns
    for pat in POS_PATTERNS:
        m = pat.search(raw)
        if m:
            groups = m.groups()
            merchant = groups[1] if len(groups) > 1 else groups[0] if groups else raw
            location = groups[2] if len(groups) > 2 else None
            return ParsedBankDescription(
                bank=None, tx_type="pos", reference=groups[0] if groups else None,
                location=location, cleaned_merchant=merchant.strip().title(),
                confidence=0.85,
            )

    # Try NIP patterns
    for pat in NIP_PATTERNS:
        m = pat.search(raw)
        if m:
            groups = m.groups()
            bank = groups[0] if groups else None
            name = groups[1] if len(groups) > 1 else None
            return ParsedBankDescription(
                bank=BANK_KEYWORDS.get(bank.lower(), bank) if bank else None,
                tx_type="nip", reference=groups[2] if len(groups) > 2 else None,
                location=None, cleaned_merchant=name.title() if name else raw.title(),
                confidence=0.80,
            )

    # Try transfer patterns
    for pat in TRANSFER_PATTERNS:
        m = pat.search(raw)
        if m:
            groups = m.groups()
            return ParsedBankDescription(
                bank=BANK_KEYWORDS.get(groups[0].lower(), groups[0]) if groups[0] else None,
                tx_type="transfer", reference=groups[1] if len(groups) > 1 else None,
                location=None, cleaned_merchant=raw.title(),
                confidence=0.75,
            )

    # Try airtime patterns
    for pat in AIRTIME_PATTERNS:
        m = pat.search(raw)
        if m:
            groups = m.groups()
            carrier = groups[0] if groups else None
            return ParsedBankDescription(
                bank=None, tx_type="airtime", reference=None,
                location=None, cleaned_merchant=f"{carrier} Airtime".title() if carrier else "Airtime Purchase",
                confidence=0.90,
            )

    # Try bill patterns
    for pat in BILL_PATTERNS:
        m = pat.search(raw)
        if m:
            groups = m.groups()
            return ParsedBankDescription(
                bank=None, tx_type="bill", reference=groups[1] if len(groups) > 1 else None,
                location=None, cleaned_merchant=f"{groups[0]} Payment".title(),
                confidence=0.90,
            )

    # Try global patterns (SQ, TST, PP)
    for pat in GLOBAL_PATTERNS:
        m = pat.search(raw)
        if m:
            merchant = m.group(1).strip()
            return ParsedBankDescription(
                bank=None, tx_type="card", reference=None,
                location=None, cleaned_merchant=merchant.title(),
                confidence=0.85,
            )

    # Detect bank name in description
    detected_bank = None
    raw_lower = raw.lower()
    for keyword, bank_name in BANK_KEYWORDS.items():
        if keyword in raw_lower:
            detected_bank = bank_name
            break

    return ParsedBankDescription(
        bank=detected_bank, tx_type=None, reference=None,
        location=None, cleaned_merchant=raw.title(),
        confidence=0.3,
    )
