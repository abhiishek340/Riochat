FROM python:3.11-slim

# Tesseract with English and Simplified Chinese language data.
RUN apt-get update \
    && apt-get install -y --no-install-recommends tesseract-ocr tesseract-ocr-eng tesseract-ocr-chi-sim \
    && rm -rf /var/lib/apt/lists/*

WORKDIR /app
COPY requirements.txt .
RUN pip install --no-cache-dir -r requirements.txt

COPY app.py pages.py ./
RUN useradd --system --uid 10001 --no-create-home --shell /usr/sbin/nologin ocr
USER ocr

ENV TESSERACT_LANG=chi_sim+eng \
    TESSERACT_PSM=3 \
    TESSERACT_OEM=3 \
    TESSERACT_RENDER_SCALE=2.0 \
    TESSERACT_GRAYSCALE=true
EXPOSE 8004
CMD ["uvicorn", "app:app", "--host", "0.0.0.0", "--port", "8004"]
