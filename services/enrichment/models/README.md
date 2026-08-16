# Brand classifier

`brand_classifier.joblib` is trained offline and committed so AtlasFlow
builds do not run sklearn on every push.

Regenerate:

```bash
cd services/enrichment
python -m scripts.generate_synthetic_data_with_labels
python -m scripts.train_brand_classifier
```
