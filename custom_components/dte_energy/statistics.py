"""Import DTE electric usage into Home Assistant long-term statistics.

The Energy dashboard only accepts external statistics for data that does not
come from an entity, and it rejects an entity/number price for those. A cost
statistic therefore has to be imported next to the energy statistic. Both are
cumulative ``sum`` series, one row per meter interval.

Import is append-mostly: the last few days are re-imported on every refresh so
DTE's estimated readings can be replaced by actual ones without breaking the
running sums.
"""

from __future__ import annotations

from dataclasses import dataclass, field
from datetime import date, datetime, timedelta
import logging

from homeassistant.components.recorder.models import (
    StatisticData,
    StatisticMeanType,
    StatisticMetaData,
)
from homeassistant.components.recorder.statistics import async_add_external_statistics
from homeassistant.core import HomeAssistant
from homeassistant.helpers.storage import Store
from homeassistant.util import dt as dt_util

from .const import (
    COST_STAT_ID,
    COST_STAT_NAME,
    DOMAIN,
    ENERGY_STAT_ID,
    ENERGY_STAT_NAME,
)
from .dte import ElectricInterval
from .tariff import TZ, price_cents_at

_LOGGER = logging.getLogger(__name__)

STORE_VERSION = 1
OVERLAP = timedelta(days=3)
KEEP = timedelta(days=14)

# Bump when rates or band rules in tariff.py change, so history is re-costed.
TARIFF_VERSION = 1


@dataclass(slots=True)
class ImportResult:
    """Outcome of one statistics import."""

    rows: int
    last_start: datetime | None
    total_kwh: float
    total_cost: float
    last_reading: datetime | None


@dataclass(slots=True)
class ImportState:
    """Persisted import cursor and cumulative totals."""

    last_start: datetime | None = None
    energy_sum: float = 0.0
    cost_sum: float = 0.0
    checkpoints: list[tuple[datetime, float, float]] = field(default_factory=list)
    tariff_version: int = 0
    critical_dates: list[str] = field(default_factory=list)

    @classmethod
    def from_dict(cls, data: dict) -> "ImportState":
        last_start = data.get("last_start")
        checkpoints: list[tuple[datetime, float, float]] = []
        for item in data.get("checkpoints", []):
            try:
                checkpoints.append(
                    (
                        datetime.fromisoformat(item[0]),
                        float(item[1]),
                        float(item[2]),
                    )
                )
            except (TypeError, ValueError, IndexError):
                continue

        return cls(
            last_start=datetime.fromisoformat(last_start) if last_start else None,
            energy_sum=float(data.get("energy_sum") or 0),
            cost_sum=float(data.get("cost_sum") or 0),
            checkpoints=checkpoints,
            tariff_version=int(data.get("tariff_version") or 0),
            critical_dates=list(data.get("critical_dates") or []),
        )

    def as_dict(self) -> dict:
        return {
            "last_start": self.last_start.isoformat() if self.last_start else None,
            "energy_sum": self.energy_sum,
            "cost_sum": self.cost_sum,
            "checkpoints": [
                [start.isoformat(), energy, cost]
                for start, energy, cost in self.checkpoints
            ],
            "tariff_version": self.tariff_version,
            "critical_dates": self.critical_dates,
        }


async def async_import_intervals(
    hass: HomeAssistant,
    entry_id: str,
    intervals: list[ElectricInterval],
    critical_dates: frozenset[date],
    now: datetime | None = None,
) -> ImportResult:
    """Import complete intervals into the recorder's long-term statistics."""
    now = now or dt_util.utcnow()

    complete = sorted(
        (
            interval
            for interval in intervals
            if interval.end <= now
            and interval.start.minute == 0
            and interval.start.second == 0
            and interval.start.microsecond == 0
        ),
        key=lambda interval: interval.start,
    )
    if not complete:
        return ImportResult(0, None, 0.0, 0.0, None)

    store = Store(hass, STORE_VERSION, f"{DOMAIN}.{entry_id}.import_state")
    state = ImportState.from_dict(await store.async_load() or {})

    critical_key = sorted(day.isoformat() for day in critical_dates)
    if (
        state.tariff_version != TARIFF_VERSION
        or state.critical_dates != critical_key
    ):
        # Rates or critical-event dates changed: re-cost the whole feed.
        state = ImportState()

    anchor = _select_anchor(state, complete)
    if anchor is None:
        after: datetime | None = None
        energy_total = 0.0
        cost_total = 0.0
    else:
        after, energy_total, cost_total = anchor

    rows = [
        interval for interval in complete if after is None or interval.start > after
    ]

    last_reading = complete[-1].start
    if not rows:
        return ImportResult(
            0,
            state.last_start,
            state.energy_sum,
            state.cost_sum,
            last_reading,
        )

    energy_rows: list[StatisticData] = []
    cost_rows: list[StatisticData] = []
    checkpoints: list[tuple[datetime, float, float]] = []

    for interval in rows:
        local = interval.start.astimezone(TZ)
        rate_cents = price_cents_at(local, critical_dates)
        cost = interval.kwh * rate_cents / 100

        energy_total += interval.kwh
        cost_total += cost

        energy_rows.append(
            StatisticData(start=interval.start, state=interval.kwh, sum=energy_total)
        )
        cost_rows.append(
            StatisticData(start=interval.start, state=cost, sum=cost_total)
        )
        checkpoints.append((interval.start, energy_total, cost_total))

    async_add_external_statistics(
        hass,
        _metadata(ENERGY_STAT_ID, ENERGY_STAT_NAME, "kWh", "energy"),
        energy_rows,
    )
    async_add_external_statistics(
        hass,
        _metadata(COST_STAT_ID, COST_STAT_NAME, "USD", None),
        cost_rows,
    )

    state.last_start = rows[-1].start
    state.energy_sum = energy_total
    state.cost_sum = cost_total
    state.checkpoints = _merge_checkpoints(
        state.checkpoints, checkpoints, state.last_start
    )
    state.tariff_version = TARIFF_VERSION
    state.critical_dates = critical_key
    await store.async_save(state.as_dict())

    _LOGGER.debug(
        "Imported %d DTE intervals (%s .. %s); %.3f kWh, $%.2f",
        len(rows),
        rows[0].start.isoformat(),
        rows[-1].start.isoformat(),
        energy_total,
        cost_total,
    )

    return ImportResult(
        len(rows), state.last_start, state.energy_sum, state.cost_sum, last_reading
    )


def _select_anchor(
    state: ImportState, intervals: list[ElectricInterval]
) -> tuple[datetime, float, float] | None:
    """Pick the cumulative anchor to re-import from.

    Returns ``None`` for a first import. Otherwise returns the datetime whose
    cumulative totals are already known and the totals themselves. A recent
    checkpoint is preferred so revised readings are picked up; if none is
    usable, import resumes after the last imported interval.
    """
    if state.last_start is None:
        return None

    target = state.last_start - OVERLAP
    candidates = [
        checkpoint for checkpoint in state.checkpoints if checkpoint[0] <= target
    ]

    if candidates:
        anchor = max(candidates, key=lambda checkpoint: checkpoint[0])
        if intervals and anchor[0] >= intervals[0].start:
            return anchor

    return (state.last_start, state.energy_sum, state.cost_sum)


def _merge_checkpoints(
    existing: list[tuple[datetime, float, float]],
    new: list[tuple[datetime, float, float]],
    last_start: datetime,
) -> list[tuple[datetime, float, float]]:
    """Merge checkpoints, keeping the most recent ones only."""
    merged = {checkpoint[0]: checkpoint for checkpoint in existing}
    for checkpoint in new:
        merged[checkpoint[0]] = checkpoint

    keep_from = last_start - KEEP
    return sorted(
        (checkpoint for checkpoint in merged.values() if checkpoint[0] >= keep_from),
        key=lambda checkpoint: checkpoint[0],
    )


def _metadata(
    statistic_id: str,
    name: str,
    unit: str,
    unit_class: str | None,
) -> StatisticMetaData:
    return StatisticMetaData(
        mean_type=StatisticMeanType.NONE,
        has_sum=True,
        name=name,
        source=DOMAIN,
        statistic_id=statistic_id,
        unit_of_measurement=unit,
        unit_class=unit_class,
    )
