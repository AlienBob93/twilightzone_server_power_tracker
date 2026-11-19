"""Parse DTE Energy's Green Button usage export.

The share link returns a NAESB ESPI Atom feed. DTE publishes two series:
electricity in watt-hours at hourly resolution (uom 72) and gas in CCF at daily
resolution. This module only extracts the electric series.
"""

from __future__ import annotations

from dataclasses import dataclass
from datetime import UTC, datetime, timedelta
import logging
import xml.etree.ElementTree as ET

ATOM = "{http://www.w3.org/2005/Atom}"
ESPI = "{http://naesb.org/espi}"

# ESPI unit of measure for watt-hours.
UOM_WATT_HOUR = "72"

_LOGGER = logging.getLogger(__name__)


class DteParseError(ValueError):
    """Raised when the feed does not contain a usable electric series."""


@dataclass(frozen=True, slots=True)
class ElectricInterval:
    """One electric meter interval."""

    start: datetime  # timezone-aware, UTC
    duration_s: int
    kwh: float
    quality: int | None = None

    @property
    def end(self) -> datetime:
        """Return the end of the interval."""
        return self.start + timedelta(seconds=self.duration_s)


def parse_electric(xml_data: bytes | str) -> list[ElectricInterval]:
    """Return the electric intervals from a Green Button Atom feed.

    Raises :class:`DteParseError` when the document cannot be parsed or has no
    electric readings.
    """
    try:
        root = ET.fromstring(xml_data)
    except ET.ParseError as err:
        raise DteParseError(f"invalid XML: {err}") from err

    entries = root.findall(f"{ATOM}entry")
    if not entries:
        raise DteParseError("the feed has no entries")

    electric_entries = [entry for entry in entries if _is_electric_entry(entry)]
    if not electric_entries:
        electric_entries = [entry for entry in entries if _has_hourly_readings(entry)]

    if not electric_entries:
        raise DteParseError("the feed has no electric interval readings")

    multiplier = _electric_multiplier(entries)
    scale = (10**multiplier) / 1000  # watt-hours -> kWh

    readings: dict[datetime, ElectricInterval] = {}

    for entry in electric_entries:
        for block in entry.iter(f"{ESPI}IntervalBlock"):
            for reading in block.findall(f"{ESPI}IntervalReading"):
                start = _epoch(reading.findtext(f"{ESPI}timePeriod/{ESPI}start"))
                duration = _int(reading.findtext(f"{ESPI}timePeriod/{ESPI}duration"), 3600)
                value = _float(reading.findtext(f"{ESPI}value"))
                if start is None or value is None or duration <= 0:
                    continue

                if start in readings:
                    _LOGGER.debug(
                        "DTE served more than one reading for %s; keeping the last",
                        start.isoformat(),
                    )

                quality = _int(
                    reading.findtext(f"{ESPI}ReadingQuality/{ESPI}quality"), None
                )
                readings[start] = ElectricInterval(
                    start=start,
                    duration_s=duration,
                    kwh=value * scale,
                    quality=quality,
                )

    if not readings:
        raise DteParseError("the feed has no usable electric interval readings")

    return sorted(readings.values(), key=lambda interval: interval.start)


def _is_electric_entry(entry: ET.Element) -> bool:
    """Return True for the entry that holds the electric interval blocks."""
    for link in entry.findall(f"{ATOM}link"):
        href = (link.get("href") or "").rstrip("/").lower()
        if link.get("rel") == "self" and href.endswith("/kwh"):
            return True

    title = (entry.findtext(f"{ATOM}title") or "").strip().lower()
    return title.startswith("electric readings")


def _has_hourly_readings(entry: ET.Element) -> bool:
    """Fallback detector: an entry whose readings are hourly."""
    for reading in entry.iter(f"{ESPI}IntervalReading"):
        duration = _int(reading.findtext(f"{ESPI}timePeriod/{ESPI}duration"), 0)
        if duration == 3600:
            return True
    return False


def _electric_multiplier(entries: list[ET.Element]) -> int:
    """Return the power-of-ten multiplier for the electric reading type."""
    for entry in entries:
        content = entry.find(f"{ATOM}content")
        if content is None:
            continue
        reading_type = content.find(f"{ESPI}ReadingType")
        if reading_type is None:
            continue
        if reading_type.findtext(f"{ESPI}uom") != UOM_WATT_HOUR:
            continue
        return _int(reading_type.findtext(f"{ESPI}powerOfTenMultiplier"), 0) or 0
    return 0


def _epoch(text: str | None) -> datetime | None:
    value = _float(text)
    if value is None:
        return None
    return datetime.fromtimestamp(value, tz=UTC)


def _float(text: str | None) -> float | None:
    if text is None:
        return None
    try:
        return float(text.strip())
    except ValueError:
        return None


def _int(text: str | None, default: int | None) -> int | None:
    if text is None:
        return default
    try:
        return int(float(text.strip()))
    except ValueError:
        return default
