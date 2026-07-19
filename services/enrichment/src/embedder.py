"""
Transaction Enrichment Pipeline — Embedding Generator.

Generates sentence embeddings for transactions to enable semantic search
and similarity matching. Uses sentence-transformers when available,
falls back to a simple TF-IDF-based embedding.
"""

import hashlib
import math
from typing import Optional


# Simple hash-based embedding as fallback (deterministic, 128-dim)
def _hash_embedding(text: str, dim: int = 128) -> list[float]:
    """Generate a deterministic pseudo-embedding from text using hashing.

    Not semantically meaningful, but enables dedup and grouping by text similarity.
    """
    vec = [0.0] * dim
    words = text.lower().split()
    for word in words:
        h = int(hashlib.md5(word.encode()).hexdigest(), 16)
        idx = h % dim
        vec[idx] += 1.0
        # Use next bytes for second component
        h2 = int(hashlib.sha1(word.encode()).hexdigest(), 16)
        idx2 = h2 % dim
        vec[idx2] += 0.5

    # L2 normalize
    norm = math.sqrt(sum(x * x for x in vec))
    if norm > 0:
        vec = [x / norm for x in vec]
    return vec


# Lazy-loaded sentence transformer model
_model = None
_model_name = "all-MiniLM-L6-v2"


def _get_model():
    """Lazy-load the sentence-transformers model."""
    global _model
    if _model is not None:
        return _model
    try:
        from sentence_transformers import SentenceTransformer
        _model = SentenceTransformer(_model_name)
        return _model
    except (ImportError, Exception):
        return None


def generate_embedding(text: str) -> list[float]:
    """Generate a sentence embedding for the given text.

    Uses sentence-transformers (all-MiniLM-L6-v2, 384-dim) when available.
    Falls back to a hash-based pseudo-embedding.
    """
    model = _get_model()
    if model is not None:
        try:
            embedding = model.encode(text, normalize_embeddings=True)
            return embedding.tolist()
        except Exception:
            pass
    return _hash_embedding(text)


def generate_batch_embeddings(texts: list[str]) -> list[list[float]]:
    """Generate embeddings for a batch of texts."""
    model = _get_model()
    if model is not None:
        try:
            embeddings = model.encode(texts, normalize_embeddings=True, batch_size=32)
            return [e.tolist() for e in embeddings]
        except Exception:
            pass
    return [_hash_embedding(t) for t in texts]


def embedding_dim() -> int:
    """Return the dimension of generated embeddings."""
    model = _get_model()
    if model is not None:
        return 384  # all-MiniLM-L6-v2
    return 128  # hash fallback
