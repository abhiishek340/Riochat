FROM python:3.11-slim

# Libraries the onnx runtime and OpenCV wheels link against.
RUN apt-get update \
    && apt-get install -y --no-install-recommends libglib2.0-0 libgl1 libgomp1 \
    && rm -rf /var/lib/apt/lists/*

WORKDIR /app
COPY requirements.txt .
RUN pip install --no-cache-dir -r requirements.txt

COPY app.py pages.py ./
RUN useradd --system --uid 10001 --no-create-home --shell /usr/sbin/nologin ocr
USER ocr

ENV RAPIDOCR_RENDER_SCALE=1.25
EXPOSE 8002
CMD ["uvicorn", "app:app", "--host", "0.0.0.0", "--port", "8002"]
