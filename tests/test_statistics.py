"""Regression tests for statistics rebuild, overlap and cursor behavior.

Home Assistant is stubbed so these run with the standard library only.
"""

from __future__ import annotations

from datetime import UTC, date, datetime, timedelta
import importlib
from pathlib import Path
import sys
import types
import unittest

# --- minimal Home Assistant stubs, installed before the component import ---
def _module(name: str) -> types.ModuleType:
    module = types.ModuleType(name)
    sys.modules[name] = module
    return module


_homeassistant = _module("homeassistant")
_homeassistant.__path__ = []
_components = _module("homeassistant.components")
_components.__path__ = []
_recorder = _module("homeassistant.components.recorder")
_recorder.__path__ = []
_models = _module("homeassistant.components.recorder.models")
_recorder_statistics = _module("homeassistant.components.recorder.statistics")
_core = _module("homeassistant.core")
_helpers = _module("homeassistant.helpers")
_helpers.__path__ = []
_storage = _module("homeassistant.helpers.storage")
_util = _module("homeassistant.util")
_util.__path__ = []
_dt_util = _module("homeassistant.util.dt")


class _StatisticMeanType:
    NONE = 0


_models.StatisticMeanType = _StatisticMeanType
_models.StatisticMetaData = dict
_models.StatisticData = dict
_core.HomeAssistant = object

CALLS: list[tuple[dict, list[dict]]] = []
CLEARS: list[list[str]] = []
RANGES: list[tuple[list[str], datetime, datetime]] = []
FAIL_CONFIRM = False
FAIL_CLEAR = False


def _async_add_external_statistics(hass, metadata, statistics) -> None:
    CALLS.append((metadata, list(statistics)))


def _clear_statistics(instance, statistic_ids) -> None:
    CLEARS.append(list(statistic_ids))
    if FAIL_CLEAR:
        raise RuntimeError("clear failed")
    for statistic_id in statistic_ids:
        CALLS[:] = [
            (metadata, rows)
            for metadata, rows in CALLS
            if metadata["statistic_id"] != statistic_id
        ]


def _clear_statistics_range(instance, statistic_ids, start, end) -> None:
    RANGES.append((list(statistic_ids), start, end))
    for index, (metadata, rows) in enumerate(CALLS):
        if metadata["statistic_id"] in statistic_ids:
            CALLS[index] = (
                metadata,
                [row for row in rows if not (start <= row["start"] < end)],
            )


def _get_last_statistics(hass, number, statistic_id, convert, types):
    if FAIL_CONFIRM:
        return {}
    last = None
    for metadata, rows in CALLS:
        if metadata["statistic_id"] == statistic_id and rows:
            last = rows[-1]
    if last is None:
        return {}
    return {statistic_id: [{"start": last["start"].timestamp(), "sum": last["sum"]}]}


_recorder_statistics.async_add_external_statistics = _async_add_external_statistics
_recorder_statistics.clear_statistics = _clear_statistics
_recorder_statistics.clear_statistics_range = _clear_statistics_range
_recorder_statistics.get_last_statistics = _get_last_statistics
_recorder.get_instance = lambda hass: hass

NOW = datetime(2026, 2, 1, tzinfo=UTC)


class FakeStore:
    """In-memory replacement for homeassistant.helpers.storage.Store."""

    DB: dict[str, dict] = {}

    def __init__(self, hass, version, key):
        self.key = key

    async def async_load(self):
        return self.DB.get(self.key)

    async def async_save(self, data):
        self.DB[self.key] = data


class FakeHass:
    """The only Home Assistant method the statistics code needs."""

    async def async_add_executor_job(self, function, *args):
        return function(*args)


_storage.Store = FakeStore
_dt_util.utcnow = lambda: NOW

COMPONENT_PATH = (
    Path(__file__).resolve().parents[1] / "custom_components" / "dte_energy"
)
_package = types.ModuleType("dte_energy")
_package.__path__ = [str(COMPONENT_PATH)]
sys.modules["dte_energy"] = _package

stats = importlib.import_module("dte_energy.statistics")

from dte_energy.const import (  # noqa: E402
    GAS_COST_STAT_ID,
    GAS_STAT_ID,
)
from dte_energy.dte import ElectricInterval, GasInterval  # noqa: E402
from dte_energy.tariff import TZ  # noqa: E402


def electric(day: int, kwh: float = 1.0) -> ElectricInterval:
    start = (
        datetime(2026, 1, 1, 16, tzinfo=TZ).astimezone(UTC)
        + timedelta(days=day)
    )
    return ElectricInterval(start, 3600, kwh)


def gas(day: int, ccf: float = 1.0) -> GasInterval:
    start = datetime(2026, 1, 1, 5, tzinfo=UTC) + timedelta(days=day)
    return GasInterval(start, 86400, ccf)


class StatisticsImportTest(unittest.IsolatedAsyncioTestCase):
    """Exercise the cursor, rebuild and overlap paths."""

    def setUp(self) -> None:
        global FAIL_CONFIRM, FAIL_CLEAR
        FAIL_CONFIRM = False
        FAIL_CLEAR = False
        FakeStore.DB.clear()
        CALLS.clear()
        CLEARS.clear()
        RANGES.clear()
        self.hass = FakeHass()

    async def test_initial_import_saves_cursor(self) -> None:
        result = await stats.async_import_intervals(
            self.hass, "e1", [electric(day) for day in range(3)], frozenset(), now=NOW
        )

        self.assertEqual(result.rows, 3)
        self.assertIn("dte_energy.e1.import_state", FakeStore.DB)
        self.assertEqual(CLEARS, [])
        self.assertEqual(RANGES, [])

    async def test_reimport_clears_the_overlap_window(self) -> None:
        intervals = [electric(day) for day in range(10)]
        await stats.async_import_intervals(
            self.hass, "e1", intervals, frozenset(), now=NOW
        )
        CALLS.clear()
        CLEARS.clear()
        RANGES.clear()

        # Drop the reading whose local date is Jan 9 (day index 8).
        without_day_8 = [
            interval
            for interval in intervals
            if interval.start.astimezone(TZ).day != 9
        ]
        result = await stats.async_import_intervals(
            self.hass, "e1", without_day_8, frozenset(), now=NOW
        )

        self.assertEqual(result.rows, 2)
        self.assertEqual(len(RANGES), 1)
        statistic_ids, start, end = RANGES[0]
        self.assertEqual(set(statistic_ids), set(stats._electric_stat_ids()))
        self.assertEqual(start, electric(6).start + timedelta(hours=1))
        self.assertEqual(end, electric(9).start + timedelta(hours=1))

    async def test_full_rebuild_clears_all_electric_statistics(self) -> None:
        intervals = [electric(day) for day in range(10)]
        await stats.async_import_intervals(
            self.hass, "e1", intervals, frozenset(), now=NOW
        )
        CALLS.clear()
        CLEARS.clear()
        RANGES.clear()

        result = await stats.async_import_intervals(
            self.hass,
            "e1",
            intervals,
            frozenset({date(2026, 1, 5)}),
            now=NOW,
        )

        self.assertEqual(result.rows, 10)
        self.assertEqual(len(CLEARS), 1)
        self.assertEqual(set(CLEARS[0]), set(stats._electric_stat_ids()))
        self.assertEqual(RANGES, [])

    async def test_clear_failure_keeps_the_cursor(self) -> None:
        global FAIL_CLEAR
        await stats.async_import_intervals(
            self.hass, "e1", [electric(0)], frozenset(), now=NOW
        )
        key = "dte_energy.e1.import_state"
        before = dict(FakeStore.DB[key])
        FAIL_CLEAR = True

        with self.assertRaises(RuntimeError):
            await stats.async_import_intervals(
                self.hass,
                "e1",
                [electric(0)],
                frozenset({date(2026, 1, 5)}),
                now=NOW,
            )

        self.assertEqual(FakeStore.DB[key], before)

    async def test_confirmation_failure_keeps_the_cursor(self) -> None:
        global FAIL_CONFIRM
        await stats.async_import_intervals(
            self.hass, "e1", [electric(0)], frozenset(), now=NOW
        )
        key = "dte_energy.e1.import_state"
        before = dict(FakeStore.DB[key])

        old_timeout = stats.CONFIRM_TIMEOUT
        old_delay = stats.CONFIRM_INITIAL_DELAY
        stats.CONFIRM_TIMEOUT = 0.05
        stats.CONFIRM_INITIAL_DELAY = 0.01
        FAIL_CONFIRM = True
        try:
            with self.assertRaises(RuntimeError):
                await stats.async_import_intervals(
                    self.hass,
                    "e1",
                    [electric(0), electric(1)],
                    frozenset(),
                    now=NOW,
                )
        finally:
            stats.CONFIRM_TIMEOUT = old_timeout
            stats.CONFIRM_INITIAL_DELAY = old_delay

        self.assertEqual(FakeStore.DB[key], before)

    async def test_gas_rebuild_clears_gas_statistics(self) -> None:
        intervals = [gas(day) for day in range(40)]
        await stats.async_import_gas(self.hass, "g1", intervals, now=NOW)
        CLEARS.clear()

        old_version = stats.GAS_TARIFF_VERSION
        stats.GAS_TARIFF_VERSION = old_version + 1
        try:
            await stats.async_import_gas(self.hass, "g1", intervals, now=NOW)
        finally:
            stats.GAS_TARIFF_VERSION = old_version

        self.assertEqual(CLEARS, [[GAS_STAT_ID, GAS_COST_STAT_ID]])

    async def test_gas_reimport_clears_the_overlap_window(self) -> None:
        intervals = [gas(day) for day in range(10)]
        await stats.async_import_gas(self.hass, "g1", intervals, now=NOW)
        RANGES.clear()

        without_day_8 = [
            interval
            for interval in intervals
            if interval.start.astimezone(TZ).day != 9
        ]
        rows = await stats.async_import_gas(
            self.hass, "g1", without_day_8, now=NOW
        )

        self.assertEqual(rows, 2)
        self.assertEqual(len(RANGES), 1)
        statistic_ids, start, end = RANGES[0]
        self.assertEqual(set(statistic_ids), {GAS_STAT_ID, GAS_COST_STAT_ID})
        self.assertEqual(start, gas(6).start + timedelta(hours=1))
        self.assertEqual(end, gas(9).start + timedelta(hours=1))


if __name__ == "__main__":
    unittest.main()
