"""
Transaction Enrichment Pipeline — Behavior Tagger.

Detects behavioral patterns from transaction metadata:
  - Frequency patterns (weekly, monthly, etc.)
  - Time-of-day patterns
  - Amount patterns (round amounts, typical amounts)
  - Merchant relationship patterns
"""

from dataclasses import dataclass, field
from typing import Optional, List


@dataclass
class BehaviorTag:
    tag: str
    confidence: float
    metadata: dict = field(default_factory=dict)


def detect_behavior_tags(
    cleaned_description: str,
    category_l1: Optional[str],
    category_l2: Optional[str],
    amount: Optional[float] = None,
    historical_amounts: Optional[List[float]] = None,
    historical_dates: Optional[List[str]] = None,
) -> List[BehaviorTag]:
    """Detect behavioral tags from a transaction and its history.

    Each tag is an independently computed signal. Tags are additive —
    a single transaction can have multiple behavior tags.
    """
    tags = []

    # Subscription/recurring detection based on category
    if category_l1 and category_l2:
        recurring_indicators = {
            ("Entertainment", "Streaming"),
            ("Housing", "Internet"),
            ("Housing", "Phone"),
            ("Health", "Gym"),
        }
        if (category_l1, category_l2) in recurring_indicators:
            tags.append(BehaviorTag(
                tag="likely_subscription",
                confidence=0.85,
                metadata={"reason": "category_match"},
            ))

    # Financial service tags
    if category_l1 == "Financial":
        if any(kw in cleaned_description.lower() for kw in ["transfer", "nip", "send"]):
            tags.append(BehaviorTag(tag="money_transfer", confidence=0.9))
        if any(kw in cleaned_description.lower() for kw in ["deposit", "fund", "top up"]):
            tags.append(BehaviorTag(tag="self_transfer", confidence=0.8))

    # Betting/gambling detection
    if category_l2 in ("Betting",) or any(kw in cleaned_description.lower() for kw in ["bet", "betting", "sporty", "bet9ja"]):
        tags.append(BehaviorTag(tag="gambling", confidence=0.95))

    # Round amount detection (salary, bills, subscriptions)
    if amount is not None:
        if amount == int(amount) and amount >= 1000:
            tags.append(BehaviorTag(
                tag="round_amount",
                confidence=0.7,
                metadata={"amount": amount},
            ))

    # Amount comparison with history
    if historical_amounts and amount is not None:
        avg = sum(historical_amounts) / len(historical_amounts) if historical_amounts else 0
        if avg > 0:
            ratio = amount / avg
            if ratio > 2.0:
                tags.append(BehaviorTag(
                    tag="unusually_high",
                    confidence=0.8,
                    metadata={"ratio": round(ratio, 2), "avg": round(avg, 2)},
                ))
            elif ratio < 0.3:
                tags.append(BehaviorTag(
                    tag="unusually_low",
                    confidence=0.7,
                    metadata={"ratio": round(ratio, 2), "avg": round(avg, 2)},
                ))

    # Frequency detection from historical dates
    if historical_dates and len(historical_dates) >= 3:
        tags.extend(_detect_frequency_pattern(historical_dates))

    return tags


def _detect_frequency_pattern(dates: List[str]) -> List[BehaviorTag]:
    """Analyze date spacing to detect recurring patterns."""
    tags = []
    if len(dates) < 3:
        return tags

    # Sort dates and compute gaps (assuming YYYY-MM-DD format)
    sorted_dates = sorted(dates)
    gaps = []
    for i in range(1, len(sorted_dates)):
        try:
            d1 = _parse_date(sorted_dates[i - 1])
            d2 = _parse_date(sorted_dates[i])
            if d1 and d2:
                gaps.append((d2 - d1).days)
        except (ValueError, TypeError):
            continue

    if len(gaps) < 2:
        return tags

    avg_gap = sum(gaps) / len(gaps)

    if 5 <= avg_gap <= 9:
        tags.append(BehaviorTag(tag="weekly_pattern", confidence=0.8, metadata={"avg_days": round(avg_gap, 1)}))
    elif 12 <= avg_gap <= 18:
        tags.append(BehaviorTag(tag="biweekly_pattern", confidence=0.8, metadata={"avg_days": round(avg_gap, 1)}))
    elif 27 <= avg_gap <= 33:
        tags.append(BehaviorTag(tag="monthly_pattern", confidence=0.9, metadata={"avg_days": round(avg_gap, 1)}))

    return tags


def _parse_date(date_str: str):
    """Parse YYYY-MM-DD date string."""
    from datetime import datetime
    try:
        return datetime.strptime(date_str[:10], "%Y-%m-%d")
    except (ValueError, TypeError):
        return None
