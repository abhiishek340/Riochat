"""Page selection helpers shared in spirit by the paginating OCR services."""

from __future__ import annotations

from pathlib import Path

IMAGE_SUFFIXES = frozenset({".jpg", ".jpeg", ".png", ".webp", ".gif", ".bmp", ".tif", ".tiff"})


def is_image(suffix: str) -> bool:
    """Return True when the file extension names a single-page image format."""
    return suffix.lower() in IMAGE_SUFFIXES


def suffix_of(filename: str | None) -> str:
    """Return the lower-case extension of an upload, defaulting to ``.pdf``."""
    suffix = Path(filename or "").suffix.lower()
    return suffix or ".pdf"


def parse_page_ranges(spec: str | None, total: int) -> list[int]:
    """Turn a page range expression into a sorted list of unique 1-based pages.

    The expression is a comma separated list of items, each a page number or
    ``a-b``. Blank items and items that are not numbers are ignored, as are
    starts below 1. An end below its start collapses to the start, ranges are
    clipped to ``total`` and single pages beyond ``total`` are dropped. When
    nothing valid remains (or the expression is empty) every page is selected.
    A document without pages yields an empty list.
    """
    if total <= 0:
        return []
    selected: set[int] = set()
    for raw in (spec or "").split(","):
        item = raw.strip()
        if not item:
            continue
        first, dash, last = item.partition("-")
        try:
            start = int(first.strip())
            end = int(last.strip()) if dash else start
        except ValueError:
            continue
        if start <= 0:
            continue
        if end < start:
            end = start
        end = min(end, total)
        selected.update(range(start, end + 1))
    if not selected:
        return list(range(1, total + 1))
    return sorted(selected)
