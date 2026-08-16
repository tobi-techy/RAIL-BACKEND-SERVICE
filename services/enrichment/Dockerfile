FROM python:3.13-slim

WORKDIR /app

COPY requirements.txt .
RUN pip install --no-cache-dir -r requirements.txt

COPY . .

# Model is trained offline and committed at models/brand_classifier.joblib.
# Training in the image made every deploy wait on sklearn on a standard builder.
RUN test -f models/brand_classifier.joblib

# AtlasFlow health probes port 3000 by default. docker-compose sets PORT=8090.
ENV PORT=3000
EXPOSE 3000

CMD ["sh", "-c", "exec uvicorn app:app --host 0.0.0.0 --port ${PORT:-3000}"]
