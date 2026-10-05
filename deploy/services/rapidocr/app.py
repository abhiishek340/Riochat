"""RapidOCR text extraction service.

POST /ocr  multipart form: file (required), page_ranges (optional), prompt
(accepted and ignored). Answers 200 with the recognised text per page, or 204
when nothing was recognised. GET /healthz reports liveness.
"""

from __future__ import annotations

import os
import shutil
import tempfile
import threading
from pathlib import Path
from typing import Any

import numpy as np
import pypdfium2 as pdfium
from fastapi import FastAPI, File, Form, Response, UploadFile
from PIL import Image, ImageOps
from rapidocr_onnxruntime import RapidOCR

from pages import is_image, parse_page_ranges, suffix_of

app = FastAPI(title="riochat-rapidocr")

_engine: RapidOCR | None = None
_engine_lock = threading.Lock()


def render_scale() -> float:
    """Return RAPIDOCR_RENDER_SCALE clamped to [1.0, 2.0] (default 1.25)."""
    try:
        value = float(os.environ.get("RAPIDOCR_RENDER_SCALE", "1.25"))
    except ValueError:
        value = 1.25
    return min(2.0, max(1.0, value))


def recognise(image: Image.Image) -> str:
    """Run OCR on one page image and return trimmed lines joined by newlines."""
    global _engine
    pixels = np.array(image.convert("RGB"))
    with _engine_lock:
        if _engine is None:
            _engine = RapidOCR()
        found: Any = _engine(pixels)[0]
    lines = [str(item[1]).strip() for item in (found or [])]
    return "\n".join(line for line in lines if line)


def page_images(path: Path, suffix: str, spec: str | None):
    """Yield (page number, PIL image) for the pages selected by ``spec``."""
    if is_image(suffix):
        with Image.open(path) as source:
            yield 1, ImageOps.exif_transpose(source).convert("RGB")
        return
    document = pdfium.PdfDocument(str(path))
    try:
        scale = render_scale()
        for number in parse_page_ranges(spec, len(document)):
            page = document[number - 1]
            try:
                yield number, page.render(scale=scale).to_pil()
            finally:
                page.close()
    finally:
        document.close()


@app.get("/healthz")
def healthz() -> dict[str, str]:
    return {"status": "ok"}


@app.post("/ocr")
def ocr(
    file: UploadFile = File(...),
    page_ranges: str | None = Form(None),
    prompt: str | None = Form(None),
    pages: str | None = Form(None),
):
    # `prompt` is part of the shared service contract and is ignored; `pages`
    # is accepted as an alias of `page_ranges`.
    spec = page_ranges if page_ranges else pages
    suffix = suffix_of(file.filename)
    spooled = tempfile.NamedTemporaryFile(prefix="rapidocr-", suffix=suffix, delete=False)
    path = Path(spooled.name)
    try:
        with spooled:
            shutil.copyfileobj(file.file, spooled)
        results = [
            {"page_number": number, "text": recognise(image)}
            for number, image in page_images(path, suffix, spec)
        ]
    finally:
        path.unlink(missing_ok=True)
    if not any(item["text"].strip() for item in results):
        return Response(status_code=204)
    return {"rendered_pages": len(results), "pages": results}
