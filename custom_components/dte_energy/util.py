"""Small helpers shared by the config flow and integration setup."""

from __future__ import annotations

from hashlib import sha256


def url_unique_id(url: str) -> str:
    """Return a stable, non-secret unique ID for a DTE share link."""
    return sha256(url.strip().encode("utf-8")).hexdigest()
