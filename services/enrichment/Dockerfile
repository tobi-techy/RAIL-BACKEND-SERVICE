FROM python:3.13-slim

WORKDIR /app

COPY requirements.txt .
RUN pip install --no-cache-dir -r requirements.txt

COPY . .

# Generate training data and train model at build time
RUN python -m scripts.generate_synthetic_data_with_labels && \
    python -m scripts.train_brand_classifier

EXPOSE 8090

CMD ["uvicorn", "app:app", "--host", "0.0.0.0", "--port", "8090"]
