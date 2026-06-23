"""
Train brand classifier: char n-gram TF-IDF + calibrated logistic regression.
Ported from d-daemon/transaction-enrichment-ml.
"""

from pathlib import Path

import joblib
import pandas as pd
from sklearn.calibration import CalibratedClassifierCV
from sklearn.feature_extraction.text import TfidfVectorizer
from sklearn.linear_model import LogisticRegression
from sklearn.model_selection import train_test_split
from sklearn.pipeline import Pipeline

DATA_PATH = Path("data/brand_training.csv")
MODEL_PATH = Path("models/brand_classifier.joblib")


def main():
    df = pd.read_csv(DATA_PATH)
    X = df["cleaned"].astype(str)
    y = df["BRAND"].astype(str)

    X_train, X_test, y_train, y_test = train_test_split(
        X, y, test_size=0.2, stratify=y, random_state=42
    )

    model = Pipeline([
        ("tfidf", TfidfVectorizer(analyzer="char", ngram_range=(3, 5))),
        ("clf", CalibratedClassifierCV(
            LogisticRegression(max_iter=1000), cv=5, method="isotonic"
        )),
    ])

    model.fit(X_train, y_train)
    print(f"Test accuracy: {model.score(X_test, y_test):.3f}")

    MODEL_PATH.parent.mkdir(parents=True, exist_ok=True)
    joblib.dump(model, MODEL_PATH)
    print(f"Model saved to {MODEL_PATH}")


if __name__ == "__main__":
    main()
