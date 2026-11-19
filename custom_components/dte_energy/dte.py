"""Parse DTE Energy's Green Button usage export.

The share link returns a NAESB ESPI Atom feed with two series: electricity in
watt-hours at hourly resolution (uom 72) and gas in CCF at daily resolution
(uom 119). This module extracts both.
"""

from __future__ import annotations

from dataclasses import dataclass
from datetime import UTC, datetime, timedelta
import logging
import xml.etree.ElementTree as ET

ATOM = "{http://www.w3.org/2005/Atom}"
ESPI = "{http://naesb.org/espi}"

# ESPI units of measure.
UOM_WATT_HOUR = "72"
UOM_CCF = "119"

_LOGGER = logging.getLogger(__name__)


class DteParseError(ValueError):
    """Raised when the feed does not contain a usable series."""


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


@dataclass(frozen=True, slots=True)
class GasInterval:
    """One gas meter interval."""

    start: datetime  # timezone-aware, UTC
    duration_s: int
    ccf: float
    quality: int | None = None

    @property
    def end(self) -> datetime:
        """Return the end of the interval."""
        return self.start + timedelta(seconds=self.duration_s)


@dataclass(frozen=True, slots=True)
class ParsedFeed:
    """Both series from one Green Button feed."""

    electric: list[ElectricInterval]
    gas: list[GasInterval]


def parse_feed(xml_data: bytes | str) -> ParsedFeed:
    """Return the electric and gas intervals from a Green Button Atom feed.

    Raises :class:`DteParseError` when the document cannot be parsed or has no
    entries. A feed without one of the series simply returns an empty list for
    that series.
    """
    try:
        root = ET.fromstring(xml_data)
    except ET.ParseError as err:
        raise DteParseError(f"invalid XML: {err}") from err

    entries = root.findall(f"{ATOM}entry")
    if not entries:
        raise DteParseError("the feed has no entries")

    electric_entries = [
        entry for entry in entries if _is_series_entry(entry, "/kwh", "electric readings")
    ]
    if not electric_entries:
        electric_entries = [
            entry for entry in entries if _has_duration(entry, 3600)
        ]

    gas_entries = [
        entry for entry in entries if _is_series_entry(entry, "/ccf", "gas readings")
    ]
    if not gas_entries:
        gas_entries = [entry for entry in entries if _has_duration(entry, 86400)]

    electric_scale = (10 ** _multiplier(entries, UOM_WATT_HOUR)) / 1000
    gas_scale = 10 ** _multiplier(entries, UOM_CCF)

    return ParsedFeed(
        electric=_parse_electric(electric_entries, electric_scale),
        gas=_parse_gas(gas_entries, gas_scale),
    )


def parse_electric(xml_data: bytes | str) -> list[ElectricInterval]:
    """Return only the electric intervals, raising when there are none."""
    feed = parse_feed(xml_data)
    if not feed.electric:
        raise DteParseError("the feed has no electric interval readings")
    return feed.electric


def parse_gas(xml_data: bytes | str) -> list[GasInterval]:
    """Return only the gas intervals."""
    return parse_feed(xml_data).gas


def _parse_electric(
    entries: list[ET.Element], scale: float
) -> list[ElectricInterval]:
    readings: dict[datetime, ElectricInterval] = {}

    for start, duration, value, quality in _iter_readings(entries):
        if start is None or value is None or duration <= 0:
            continue
        if start in readings:
            _LOGGER.debug(
                "DTE served more than one electric reading for %s; keeping the last",
                start.isoformat(),
            )
        readings[start] = ElectricInterval(
            start=start,
            duration_s=duration,
            kwh=value * scale,
            quality=quality,
        )

    return sorted(readings.values(), key=lambda interval: interval.start)


def _parse_gas(entries: list[ET.Element], scale: float) -> list[GasInterval]:
    readings: dict[datetime, GasInterval] = {}

    for start, duration, value, quality in _iter_readings(entries):
        if start is None or value is None or duration <= 0:
            continue
        if start in readings:
            _LOGGER.debug(
                "DTE served more than one gas reading for %s; keeping the last",
                start.isoformat(),
            )
        readings[start] = GasInterval(
            start=start,
            duration_s=duration,
            ccf=value * scale,
            quality=quality,
        )

    return sorted(readings.values(), key=lambda interval: interval.start)


def _iter_readings(entries: list[ET.Element]):
    for entry in entries:
        for block in entry.iter(f"{ESPI}IntervalBlock"):
            for reading in block.findall(f"{ESPI}IntervalReading"):
                start = _epoch(reading.findtext(f"{ESPI}timePeriod/{ESPI}start"))
                duration = _int(reading.findtext(f"{ESPI}timePeriod/{ESPI}duration"), 3600)
                value = _float(reading.findtext(f"{ESPI}value"))
                quality = _int(
                    reading.findtext(f"{ESPI}ReadingQuality/{ESPI}quality"), None
                )
                yield start, duration, value, quality


def _is_series_entry(entry: ET.Element, suffix: str, title_prefix: str) -> bool:
    """Return True for the entry that holds a series' interval blocks."""
    for link in entry.findall(f"{ATOM}link"):
        href = (link.get("href") or "").rstrip("/").lower()
        if link.get("rel") == "self" and href.endswith(suffix):
            return True

    title = (entry.findtext(f"{ATOM}title") or "").strip().lower()
    return title.startswith(title_prefix)


def _has_duration(entry: ET.Element, seconds: int) -> bool:
    """Fallback detector: an entry whose readings have the given duration."""
    for reading in entry.iter(f"{ESPI}IntervalReading"):
        duration = _int(reading.findtext(f"{ESPI}timePeriod/{ESPI}duration"), 0)
        if duration == seconds:
            return True
    return False


def _multiplier(entries: list[ET.Element], uom: str) -> int:
    """Return the power-of-ten multiplier for a reading type."""
    for entry in entries:
        content = entry.find(f"{ATOM}content")
        if content is None:
            continue
        reading_type = content.find(f"{ESPI}ReadingType")
        if reading_type is None:
            continue
        if reading_type.findtext(f"{ESPI}uom") != uom:
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
