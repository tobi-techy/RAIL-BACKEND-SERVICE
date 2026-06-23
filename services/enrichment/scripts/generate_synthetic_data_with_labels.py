"""
Generate synthetic training data for brand classifier.
Ported from d-daemon/transaction-enrichment-ml.
"""

import random
from pathlib import Path

import pandas as pd
from faker import Faker

from src.brand_matcher import clean_merchant_name

fake = Faker()

BRANDS = [
    "Starbucks", "McDonalds", "FairPrice", "Grab", "Shell",
    "Apple Store", "Guardian", "Netflix", "Spotify", "Amazon",
    "Walmart", "Costco", "Target", "Uber", "Lyft",
    "Con Edison", "PG&E", "Comcast", "AT&T", "Verizon",
    "T-Mobile", "State Farm", "Geico", "Kaiser", "CVS",
    "Walgreens", "Planet Fitness", "Hulu", "Disney", "YouTube",
]


def generate_raw_merchant(brand_name: str) -> str:
    patterns = [
        lambda b: f"{b.upper()} #{random.randint(1, 999)}",
        lambda b: f"{b} {fake.city()}",
        lambda b: f"{b[:4]}*{b[4:]}",
        lambda b: f"{b.upper()}-{random.choice(['Mall', 'TST', 'HQ', 'ONLINE'])}",
        lambda b: b.upper(),
        lambda b: f"SQ *{b.upper()}",
        lambda b: f"TST*{b.upper()} {fake.city()[:8]}",
    ]
    return random.choice(patterns)(brand_name)


def generate_datasets(n: int = 8000, seed: int = 42):
    random.seed(seed)
    Faker.seed(seed)

    records = []
    for i in range(n):
        brand = random.choice(BRANDS)
        records.append({
            "cleaned": clean_merchant_name(generate_raw_merchant(brand)),
            "BRAND": brand,
        })

    df_train = pd.DataFrame(records)

    # Add "Other" reject class
    others = []
    for _ in range(3000):
        noise = fake.company()[:15]
        others.append({"cleaned": clean_merchant_name(noise), "BRAND": "Other"})

    return pd.concat([df_train, pd.DataFrame(others)], ignore_index=True)


if __name__ == "__main__":
    output_dir = Path("data")
    output_dir.mkdir(parents=True, exist_ok=True)
    df = generate_datasets()
    df.to_csv(output_dir / "brand_training.csv", index=False)
    print(f"Generated {len(df)} training samples")
