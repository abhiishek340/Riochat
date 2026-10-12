FROM python:3.11-slim

# Tesseract is Docling's OCR backend; poppler and libmagic are used for PDF
# and file-type handling.
RUN apt-get update \
    && apt-get install -y --no-install-recommends \
        tesseract-ocr tesseract-ocr-eng poppler-utils libmagic1 libgl1 libglib2.0-0 \
    && rm -rf /var/lib/apt/lists/*

WORKDIR /app
COPY requirements.txt .
RUN pip install --no-cache-dir -r requirements.txt

COPY app.py ./
# Model files are downloaded on first use into the home directory of this user.
RUN useradd --system --uid 10001 --create-home --home-dir /home/ocr --shell /usr/sbin/nologin ocr \
    && mkdir -p /home/ocr/.cache \
    && chown -R ocr:ocr /home/ocr
USER ocr
ENV HOME=/home/ocr

EXPOSE 8005
CMD ["uvicorn", "app:app", "--host", "0.0.0.0", "--port", "8005"]
