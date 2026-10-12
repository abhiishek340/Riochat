"""Docling document conversion service.

POST /ocr  multipart form: file (required); page_ranges and prompt are accepted
and ignored. The whole document is converted and returned as Markdown in
``{"text": ...}``; an empty result answers 204. GET /healthz reports liveness.
"""

from __future__ import annotations

import shutil
import tempfile
import threading
from pathlib import Path

from docling.document_converter import DocumentConverter
from fastapi import FastAPI, File, Form, Response, UploadFile

app = FastAPI(title="riochat-docling")

_converter: DocumentConverter | None = None
_converter_lock = threading.Lock()


def suffix_of(filename: str | None) -> str:
    """Return the lower-case extension of an upload, defaulting to ``.pdf``."""
    return Path(filename or "").suffix.lower() or ".pdf"


def convert(path: Path) -> str:
    global _converter
    with _converter_lock:
        if _converter is None:
            _converter = DocumentConverter()
        result = _converter.convert(str(path))
    return result.document.export_to_markdown().strip()


@app.get("/healthz")
def healthz() -> dict[str, str]:
    return {"status": "ok", "engine": "docling"}


@app.post("/ocr")
def ocr(
    file: UploadFile = File(...),
    page_ranges: str | None = Form(None),
    prompt: str | None = Form(None),
):
    spooled = tempfile.NamedTemporaryFile(
        prefix="docling-", suffix=suffix_of(file.filename), delete=False
    )
    path = Path(spooled.name)
    try:
        with spooled:
            shutil.copyfileobj(file.file, spooled)
        text = convert(path)
    finally:
        path.unlink(missing_ok=True)
    if not text:
        return Response(status_code=204)
    return {"text": text}
