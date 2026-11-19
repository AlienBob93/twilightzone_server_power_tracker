"""Import DTE electric usage into Home Assistant long-term statistics.

The Energy dashboard only accepts external statistics for data that does not
come from an entity, and it rejects an entity/number price for those. A cost
statistic therefore has to be imported next to the energy statistic. Both are
cumulative ``sum`` series, one row per meter interval.

Besides the combined series, the intervals are partitioned into the tariff
bands (off-peak, mid-peak, peak, critical). Adding one Energy dashboard grid
connection per band gives the native per-tariff breakdown: the sources table,
the usage graph and the cost graph split by rate period. The combined series is
kept for setups that only want a single connection; do not use both at once.

Import is append-mostly: the last few days are re-imported on every refresh so
DTE's estimated readings can be replaced by actual ones without breaking the
running sums.
"""

from __future__ import annotations

from dataclasses import dataclass, field
from datetime import date, datetime, timedelta
import logging

from homeassistant.components.recorder import get_instance
from homeassistant.components.recorder.models import (
    StatisticData,
    StatisticMeanType,
    StatisticMetaData,
)
from homeassistant.components.recorder.statistics import (
    async_add_external_statistics,
    clear_statistics,
)
from homeassistant.core import HomeAssistant
from homeassistant.helpers.storage import Store
from homeassistant.util import dt as dt_util

from .const import (
    BAND_LABELS,
    COST_STAT_ID,
    COST_STAT_NAME,
    DOMAIN,
    ENERGY_STAT_ID,
    ENERGY_STAT_NAME,
    GAS_COST_STAT_ID,
    GAS_COST_STAT_NAME,
    GAS_STAT_ID,
    GAS_STAT_NAME,
)
from .dte import ElectricInterval, GasInterval
from .gas_tariff import GAS_TARIFF_VERSION, cost_for_day
from .tariff import TZ, rate_at

_LOGGER = logging.getLogger(__name__)

STORE_VERSION = 1
OVERLAP = timedelta(days=3)
KEEP = timedelta(days=14)

GAS_STORE_VERSION = 1
GAS_SCHEMA_VERSION = 2
GAS_OVERLAP = timedelta(days=3)
GAS_KEEP = timedelta(days=14)

# Bump when the persisted import state changes shape, or when rates/band rules
# in tariff.py change, so the whole feed is re-costed.
SCHEMA_VERSION = 2
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
class Checkpoint:
    """Cumulative totals after the interval that starts at ``start``."""

    start: datetime
    energy: float
    cost: float
    bands: dict[str, tuple[float, float]] = field(default_factory=dict)


@dataclass(slots=True)
class ImportState:
    """Persisted import cursor and cumulative totals."""

    last_start: datetime | None = None
    energy_sum: float = 0.0
    cost_sum: float = 0.0
    band_totals: dict[str, tuple[float, float]] = field(default_factory=dict)
    checkpoints: list[Checkpoint] = field(default_factory=list)
    tariff_version: int = 0
    critical_dates: list[str] = field(default_factory=list)
    schema: int = SCHEMA_VERSION

    @classmethod
    def from_dict(cls, data: dict) -> "ImportState":
        if data.get("schema") != SCHEMA_VERSION:
            # Older state has no per-band totals. A full re-import is cheaper
            # than trying to reconstruct them.
            return cls()

        checkpoint_list: list[Checkpoint] = []
        for item in data.get("checkpoints", []):
            try:
                checkpoint_list.append(
                    Checkpoint(
                        start=datetime.fromisoformat(item[0]),
                        energy=float(item[1]),
                        cost=float(item[2]),
                        bands=_bands_from_json(item[3]),
                    )
                )
            except (TypeError, ValueError, IndexError, KeyError):
                continue

        last_start = data.get("last_start")

        return cls(
            last_start=datetime.fromisoformat(last_start) if last_start else None,
            energy_sum=float(data.get("energy_sum") or 0),
            cost_sum=float(data.get("cost_sum") or 0),
            band_totals=_bands_from_json(data.get("band_totals")),
            checkpoints=checkpoint_list,
            tariff_version=int(data.get("tariff_version") or 0),
            critical_dates=list(data.get("critical_dates") or []),
            schema=SCHEMA_VERSION,
        )

    def as_dict(self) -> dict:
        return {
            "schema": self.schema,
            "last_start": self.last_start.isoformat() if self.last_start else None,
            "energy_sum": self.energy_sum,
            "cost_sum": self.cost_sum,
            "band_totals": _bands_to_json(self.band_totals),
            "checkpoints": [
                [
                    checkpoint.start.isoformat(),
                    checkpoint.energy,
                    checkpoint.cost,
                    _bands_to_json(checkpoint.bands),
                ]
                for checkpoint in self.checkpoints
            ],
            "tariff_version": self.tariff_version,
            "critical_dates": self.critical_dates,
        }


@dataclass(slots=True)
class GasState:
    """Persisted gas import cursor and cumulative totals."""

    last_start: datetime | None = None
    total: float = 0.0
    cost_total: float = 0.0
    checkpoints: list[tuple[datetime, float, float]] = field(default_factory=list)
    tariff_version: int = 0
    schema: int = GAS_SCHEMA_VERSION

    @classmethod
    def from_dict(cls, data: dict) -> "GasState":
        if data.get("schema") != GAS_SCHEMA_VERSION:
            return cls()

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

        last_start = data.get("last_start")

        return cls(
            last_start=datetime.fromisoformat(last_start) if last_start else None,
            total=float(data.get("total") or 0),
            cost_total=float(data.get("cost_total") or 0),
            checkpoints=checkpoints,
            tariff_version=int(data.get("tariff_version") or 0),
            schema=GAS_SCHEMA_VERSION,
        )

    def as_dict(self) -> dict:
        return {
            "schema": self.schema,
            "last_start": self.last_start.isoformat() if self.last_start else None,
            "total": self.total,
            "cost_total": self.cost_total,
            "checkpoints": [
                [start.isoformat(), total, cost]
                for start, total, cost in self.checkpoints
            ],
            "tariff_version": self.tariff_version,
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
    raw_state = await store.async_load() or {}
    state = ImportState.from_dict(raw_state)

    critical_key = sorted(day.isoformat() for day in critical_dates)
    tariff_changed = (
        state.tariff_version != TARIFF_VERSION
        or raw_state.get("schema") != SCHEMA_VERSION
    )
    dates_changed = state.critical_dates != critical_key
    full_rebuild = tariff_changed or dates_changed

    if raw_state.get("last_start") and full_rebuild:
        if tariff_changed:
            # Band membership can change with the rules; rebuild all bands.
            await _async_clear_statistics(hass, list(BAND_LABELS))
        else:
            # Critical hours replace peak hours, so both series are rebuilt.
            await _async_clear_statistics(hass, ["peak", "critical"])

    if full_rebuild:
        state = ImportState()

    anchor = _select_anchor(state, complete)
    if anchor is None:
        after: datetime | None = None
        energy_total = 0.0
        cost_total = 0.0
        band_totals: dict[str, tuple[float, float]] = {}
    else:
        after = anchor.start
        energy_total = anchor.energy
        cost_total = anchor.cost
        band_totals = {
            band: (values[0], values[1]) for band, values in anchor.bands.items()
        }

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
    band_energy_rows: dict[str, list[StatisticData]] = {}
    band_cost_rows: dict[str, list[StatisticData]] = {}
    checkpoints: list[Checkpoint] = []

    band_energy = {band: values[0] for band, values in band_totals.items()}
    band_cost = {band: values[1] for band, values in band_totals.items()}

    for interval in rows:
        local = interval.start.astimezone(TZ)
        rate = rate_at(local, critical_dates)
        band = rate.band.value
        cost = interval.kwh * rate.cents_per_kwh / 100

        energy_total += interval.kwh
        cost_total += cost

        energy_rows.append(
            StatisticData(start=interval.start, state=interval.kwh, sum=energy_total)
        )
        cost_rows.append(
            StatisticData(start=interval.start, state=cost, sum=cost_total)
        )

        band_energy[band] = band_energy.get(band, 0.0) + interval.kwh
        band_cost[band] = band_cost.get(band, 0.0) + cost

        band_energy_rows.setdefault(band, []).append(
            StatisticData(
                start=interval.start, state=interval.kwh, sum=band_energy[band]
            )
        )
        band_cost_rows.setdefault(band, []).append(
            StatisticData(start=interval.start, state=cost, sum=band_cost[band])
        )

        checkpoints.append(
            Checkpoint(
                start=interval.start,
                energy=energy_total,
                cost=cost_total,
                bands={
                    name: (band_energy[name], band_cost[name])
                    for name in band_energy
                },
            )
        )

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

    for band, band_rows in band_energy_rows.items():
        label = BAND_LABELS[band]
        async_add_external_statistics(
            hass,
            _metadata(
                f"{DOMAIN}:{band}_consumption",
                f"DTE {label} grid consumption",
                "kWh",
                "energy",
            ),
            band_rows,
        )
    for band, band_rows in band_cost_rows.items():
        label = BAND_LABELS[band]
        async_add_external_statistics(
            hass,
            _metadata(
                f"{DOMAIN}:{band}_cost",
                f"DTE {label} grid cost",
                "USD",
                None,
            ),
            band_rows,
        )

    state.last_start = rows[-1].start
    state.energy_sum = energy_total
    state.cost_sum = cost_total
    state.band_totals = {
        band: (band_energy[band], band_cost[band]) for band in band_energy
    }
    state.checkpoints = _merge_checkpoints(
        state.checkpoints, checkpoints, state.last_start
    )
    state.tariff_version = TARIFF_VERSION
    state.critical_dates = critical_key
    state.schema = SCHEMA_VERSION
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


async def async_import_gas(
    hass: HomeAssistant,
    entry_id: str,
    intervals: list[GasInterval],
    now: datetime | None = None,
) -> int:
    """Import daily gas usage and cost into long-term statistics.

    Each daily reading is placed at the local start of its day (midnight), which
    is top-of-hour in UTC. Home Assistant stores hourly statistics, so the daily
    total is one row; the Energy dashboard's daily gas buckets sum it correctly.

    The monthly customer charge, IRM surcharge and Michigan sales tax are
    applied on the first imported day of each calendar month.
    """
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
        return 0

    store = Store(hass, GAS_STORE_VERSION, f"{DOMAIN}.{entry_id}.gas_state")
    state = GasState.from_dict(await store.async_load() or {})
    if state.tariff_version != GAS_TARIFF_VERSION:
        # Rates or fixed charges changed; rebuild the whole gas history.
        state = GasState()

    # Charge the monthly fixed amount on the first available day of the month.
    charge_dates: set[date] = set()
    seen_months: set[tuple[int, int]] = set()
    for interval in complete:
        day = interval.start.astimezone(TZ).date()
        key = (day.year, day.month)
        if key not in seen_months:
            seen_months.add(key)
            charge_dates.add(day)

    anchor = _select_gas_anchor(state, complete)
    if anchor is None:
        after: datetime | None = None
        total = 0.0
        cost_total = 0.0
    else:
        after, total, cost_total = anchor

    rows = [
        interval for interval in complete if after is None or interval.start > after
    ]
    if not rows:
        return 0

    usage_stats: list[StatisticData] = []
    cost_stats: list[StatisticData] = []
    checkpoints: list[tuple[datetime, float, float]] = []

    for interval in rows:
        day = interval.start.astimezone(TZ).date()
        cost = cost_for_day(day, interval.ccf, day in charge_dates)

        total += interval.ccf
        cost_total += cost

        usage_stats.append(
            StatisticData(start=interval.start, state=interval.ccf, sum=total)
        )
        cost_stats.append(
            StatisticData(start=interval.start, state=cost, sum=cost_total)
        )
        checkpoints.append((interval.start, total, cost_total))

    async_add_external_statistics(
        hass,
        _metadata(GAS_STAT_ID, GAS_STAT_NAME, "CCF", "volume"),
        usage_stats,
    )
    async_add_external_statistics(
        hass,
        _metadata(GAS_COST_STAT_ID, GAS_COST_STAT_NAME, "USD", None),
        cost_stats,
    )

    state.last_start = rows[-1].start
    state.total = total
    state.cost_total = cost_total
    state.checkpoints = _merge_gas_checkpoints(
        state.checkpoints, checkpoints, state.last_start
    )
    state.tariff_version = GAS_TARIFF_VERSION
    state.schema = GAS_SCHEMA_VERSION
    await store.async_save(state.as_dict())

    _LOGGER.debug(
        "Imported %d DTE gas intervals (%s .. %s); %.3f CCF, $%.2f",
        len(rows),
        rows[0].start.isoformat(),
        rows[-1].start.isoformat(),
        total,
        cost_total,
    )

    return len(rows)


def _select_anchor(
    state: ImportState, intervals: list[ElectricInterval]
) -> Checkpoint | None:
    """Pick the cumulative anchor to re-import from.

    Returns ``None`` for a first import. Otherwise returns the checkpoint whose
    cumulative totals are already known. A recent checkpoint is preferred so
    revised readings are picked up; if none is usable, import resumes after the
    last imported interval.
    """
    if state.last_start is None:
        return None

    target = state.last_start - OVERLAP
    candidates = [
        checkpoint for checkpoint in state.checkpoints if checkpoint.start <= target
    ]

    if candidates:
        anchor = max(candidates, key=lambda checkpoint: checkpoint.start)
        if intervals and anchor.start >= intervals[0].start:
            return anchor

    return Checkpoint(
        start=state.last_start,
        energy=state.energy_sum,
        cost=state.cost_sum,
        bands=dict(state.band_totals),
    )


def _merge_checkpoints(
    existing: list[Checkpoint],
    new: list[Checkpoint],
    last_start: datetime,
) -> list[Checkpoint]:
    """Merge checkpoints, keeping the most recent ones only."""
    merged = {checkpoint.start: checkpoint for checkpoint in existing}
    for checkpoint in new:
        merged[checkpoint.start] = checkpoint

    keep_from = last_start - KEEP
    return sorted(
        (checkpoint for checkpoint in merged.values() if checkpoint.start >= keep_from),
        key=lambda checkpoint: checkpoint.start,
    )


def _select_gas_anchor(
    state: GasState, intervals: list[GasInterval]
) -> tuple[datetime, float, float] | None:
    """Pick the gas cumulative anchor to re-import from."""
    if state.last_start is None:
        return None

    target = state.last_start - GAS_OVERLAP
    candidates = [
        checkpoint for checkpoint in state.checkpoints if checkpoint[0] <= target
    ]

    if candidates:
        anchor = max(candidates, key=lambda checkpoint: checkpoint[0])
        if intervals and anchor[0] >= intervals[0].start:
            return anchor

    return (state.last_start, state.total, state.cost_total)


def _merge_gas_checkpoints(
    existing: list[tuple[datetime, float, float]],
    new: list[tuple[datetime, float, float]],
    last_start: datetime,
) -> list[tuple[datetime, float, float]]:
    """Merge gas checkpoints, keeping the most recent ones only."""
    merged = {start: (start, total, cost) for start, total, cost in existing}
    for start, total, cost in new:
        merged[start] = (start, total, cost)

    keep_from = last_start - GAS_KEEP
    return sorted(
        (checkpoint for checkpoint in merged.values() if checkpoint[0] >= keep_from),
        key=lambda checkpoint: checkpoint[0],
    )


async def _async_clear_statistics(hass: HomeAssistant, bands: list[str]) -> None:
    """Clear the per-band statistics before rebuilding them."""
    statistic_ids = [
        f"{DOMAIN}:{band}_{kind}" for band in bands for kind in ("consumption", "cost")
    ]
    instance = get_instance(hass)
    try:
        await hass.async_add_executor_job(clear_statistics, instance, statistic_ids)
    except Exception:  # noqa: BLE001 - a failed clear must not stop the import
        _LOGGER.exception("Could not clear statistics for %s", ", ".join(bands))


def _bands_to_json(bands: dict[str, tuple[float, float]]) -> dict[str, list[float]]:
    return {band: [values[0], values[1]] for band, values in bands.items()}


def _bands_from_json(raw: dict | None) -> dict[str, tuple[float, float]]:
    if not raw:
        return {}
    bands: dict[str, tuple[float, float]] = {}
    for band, values in raw.items():
        try:
            bands[band] = (float(values[0]), float(values[1]))
        except (TypeError, ValueError, IndexError):
            continue
    return bands


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
