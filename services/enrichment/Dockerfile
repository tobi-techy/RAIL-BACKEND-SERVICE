FROM python:3.13-slim

WORKDIR /app

COPY requirements.txt .
RUN pip install --no-cache-dir -r requirements.txt

COPY . .

# Generate training data and train model at build time
RUN python -m scripts.generate_synthetic_data_with_labels && \
    python -m scripts.train_brand_classifier

# AtlasFlow health probes port 3000 by default. docker-compose sets PORT=8090.
ENV PORT=3000
EXPOSE 3000

CMD ["sh", "-c", "exec uvicorn app:app --host 0.0.0.0 --port ${PORT:-3000}"]
