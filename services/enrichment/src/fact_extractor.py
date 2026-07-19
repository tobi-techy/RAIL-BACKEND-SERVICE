"""
Transaction Enrichment Pipeline — Fact Extractor.

Extracts durable financial facts from transaction enrichment results.
These facts feed into Miriam's memory system for long-term reasoning.
"""

from dataclasses import dataclass
from typing import Optional, List


@dataclass
class TransactionFact:
    fact_type: str     # recurring_expense, merchant_preference, spending_pattern, etc.
    value: str         # human-readable fact
    confidence: float  # 0.0 - 1.0
    category: str      # memory category: habit, financial_behavior, preference, etc.


def extract_facts(
    counterparty: str,
    category_l1: Optional[str],
    category_l2: Optional[str],
    is_essential: bool,
    behavior_tags: list,
    amount: Optional[float] = None,
) -> List[TransactionFact]:
    """Extract durable financial facts from enriched transaction data.

    Each fact is designed to persist in Miriam's memory and support
    future reasoning about the user's financial life.
    """
    facts = []

    # Subscription detection
    for tag in behavior_tags:
        if tag.tag == "likely_subscription" and counterparty:
            facts.append(TransactionFact(
                fact_type="recurring_expense",
                value=f"{counterparty} subscription",
                confidence=tag.confidence,
                category="habit",
            ))

    # Merchant preference (high-confidence brand matches)
    if counterparty and category_l1:
        # Only create merchant preference facts for specific, high-confidence matches
        if category_l1 in ("Food & Drink", "Transport", "Shopping", "Entertainment"):
            facts.append(TransactionFact(
                fact_type="merchant_preference",
                value=f"User uses {counterparty}",
                confidence=0.7,
                category="preference",
            ))

    # Essential spending pattern
    if is_essential and category_l1 and counterparty:
        facts.append(TransactionFact(
            fact_type="essential_expense",
            value=f"{counterparty} is an essential expense ({category_l1})",
            confidence=0.8,
            category="financial_behavior",
        ))

    # Gambling detection
    for tag in behavior_tags:
        if tag.tag == "gambling":
            facts.append(TransactionFact(
                fact_type="gambling_activity",
                value="User places bets",
                confidence=tag.confidence,
                category="habit",
            ))

    # Unusually high spending
    for tag in behavior_tags:
        if tag.tag == "unusually_high" and counterparty:
            facts.append(TransactionFact(
                fact_type="spending_anomaly",
                value=f"Unusually high spend at {counterparty} (${tag.metadata.get('ratio', '?')}x average)",
                confidence=tag.confidence,
                category="financial_behavior",
            ))

    # Money transfer patterns
    for tag in behavior_tags:
        if tag.tag == "money_transfer":
            facts.append(TransactionFact(
                fact_type="transfer_pattern",
                value="User sends money regularly",
                confidence=tag.confidence,
                category="financial_behavior",
            ))

    # Salary/regular income detection (round large amounts from financial institutions)
    if amount and amount >= 50000 and category_l1 == "Financial":
        for tag in behavior_tags:
            if tag.tag == "round_amount" or tag.tag == "monthly_pattern":
                facts.append(TransactionFact(
                    fact_type="income_pattern",
                    value="Regular income detected",
                    confidence=0.75,
                    category="income_pattern",
                ))
                break

    return facts
