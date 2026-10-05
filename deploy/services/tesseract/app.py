"""Tesseract text extraction service.

POST /ocr  multipart form: file (required), page_ranges (optional), prompt
(accepted and ignored). Answers 200 with the recognised text per page, or 204
when nothing was recognised. GET /healthz reports liveness and the engine
settings in use.
"""

from __future__ import annotations

import os
import shutil
import tempfile
from pathlib import Path

import pypdfium2 as pdfium
import pytesseract
from fastapi import FastAPI, File, Form, Response, UploadFile
from PIL import Image, ImageOps

from pages import is_image, parse_page_ranges, suffix_of

app = FastAPI(title="riochat-tesseract")


def _int_env(name: str, default: int) -> int:
    try:
        return int(os.environ.get(name, str(default)))
    except ValueError:
        return default


def lang() -> str:
    return os.environ.get("TESSERACT_LANG", "").strip() or "chi_sim+eng"


def psm() -> int:
    return _int_env("TESSERACT_PSM", 3)


def oem() -> int:
    return _int_env("TESSERACT_OEM", 3)


def render_scale() -> float:
    """Return TESSERACT_RENDER_SCALE clamped to [1.0, 3.0] (default 2.0)."""
    try:
        value = float(os.environ.get("TESSERACT_RENDER_SCALE", "2.0"))
    except ValueError:
        value = 2.0
    return min(3.0, max(1.0, value))


def grayscale() -> bool:
    """TESSERACT_GRAYSCALE is on unless set to 0, false or no."""
    return os.environ.get("TESSERACT_GRAYSCALE", "true").strip().lower() not in {
        "0",
        "false",
        "no",
    }


def prepare(image: Image.Image) -> Image.Image:
    """Honour EXIF orientation, then convert to grayscale (or RGB)."""
    upright = ImageOps.exif_transpose(image)
    return upright.convert("L" if grayscale() else "RGB")


def recognise(image: Image.Image) -> str:
    """Run OCR on one page image; lines are trimmed and blank lines removed."""
    raw = pytesseract.image_to_string(
        prepare(image), lang=lang(), config=f"--psm {psm()} --oem {oem()}"
    )
    lines = [line.strip() for line in raw.splitlines()]
    return "\n".join(line for line in lines if line)


def page_images(path: Path, suffix: str, spec: str | None):
    """Yield (page number, PIL image) for the pages selected by ``spec``."""
    if is_image(suffix):
        with Image.open(path) as source:
            source.load()
            yield 1, source.copy()
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
def healthz() -> dict[str, object]:
    return {
        "status": "ok",
        "engine": "tesseract",
        "lang": lang(),
        "psm": psm(),
        "oem": oem(),
    }


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
    spooled = tempfile.NamedTemporaryFile(prefix="tesseract-", suffix=suffix, delete=False)
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
