FROM python:3.11-slim

WORKDIR /app

# PaddleOCR/OpenCV need these shared libs at runtime.
RUN apt-get update && apt-get install -y --no-install-recommends \
    libgl1 \
    libglib2.0-0 \
    libgomp1 \
    && rm -rf /var/lib/apt/lists/*

COPY requirements.txt .
RUN pip install --no-cache-dir -r requirements.txt

COPY . .

# Warm the PaddleOCR model cache at build time so first request is fast.
RUN python -c "from paddleocr import PaddleOCR; PaddleOCR(use_angle_cls=True, lang='en', show_log=False)" || true

# AtlasFlow health probes port 3000 by default. docker-compose sets PORT=8091.
ENV PORT=3000
EXPOSE 3000

CMD ["sh", "-c", "exec uvicorn app:app --host 0.0.0.0 --port ${PORT:-3000}"]
