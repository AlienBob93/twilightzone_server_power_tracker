"""Tests for the DTE tariff rules (stdlib only, no Home Assistant needed)."""

from __future__ import annotations

from datetime import date, datetime
import importlib.util
from pathlib import Path
import sys
import unittest

TARIFF_PATH = (
    Path(__file__).resolve().parents[1]
    / "custom_components"
    / "dte_energy"
    / "tariff.py"
)
_spec = importlib.util.spec_from_file_location("dte_tariff", TARIFF_PATH)
assert _spec and _spec.loader
tariff = importlib.util.module_from_spec(_spec)
sys.modules[_spec.name] = tariff
_spec.loader.exec_module(tariff)

CONST_PATH = (
    Path(__file__).resolve().parents[1]
    / "custom_components"
    / "dte_energy"
    / "const.py"
)
_const_spec = importlib.util.spec_from_file_location("dte_const", CONST_PATH)
assert _const_spec and _const_spec.loader
const = importlib.util.module_from_spec(_const_spec)
sys.modules[_const_spec.name] = const
_const_spec.loader.exec_module(const)

TZ = tariff.TZ


def at(year: int, month: int, day: int, hour: int, minute: int = 0) -> datetime:
    return datetime(year, month, day, hour, minute, tzinfo=TZ)


class TariffTest(unittest.TestCase):
    """Rate periods, holidays, seasons and the plan switch."""

    def rate(self, when: datetime, critical=frozenset()) -> float:
        return tariff.price_cents_at(when, critical)

    def test_dynamic_peak_pricing_weekday(self) -> None:
        # 2026-07-15 is a Wednesday.
        self.assertAlmostEqual(self.rate(at(2026, 7, 15, 6, 59)), 14.31)
        self.assertAlmostEqual(self.rate(at(2026, 7, 15, 7, 0)), 18.61)
        self.assertAlmostEqual(self.rate(at(2026, 7, 15, 14, 59)), 18.61)
        self.assertAlmostEqual(self.rate(at(2026, 7, 15, 15, 0)), 26.92)
        self.assertAlmostEqual(self.rate(at(2026, 7, 15, 18, 59)), 26.92)
        self.assertAlmostEqual(self.rate(at(2026, 7, 15, 19, 0)), 18.61)
        self.assertAlmostEqual(self.rate(at(2026, 7, 15, 22, 59)), 18.61)
        self.assertAlmostEqual(self.rate(at(2026, 7, 15, 23, 0)), 14.31)

    def test_critical_event_overlay(self) -> None:
        critical = frozenset({date(2026, 7, 15)})
        self.assertAlmostEqual(self.rate(at(2026, 7, 15, 16, 0), critical), 105.00)
        self.assertAlmostEqual(self.rate(at(2026, 7, 15, 19, 0), critical), 18.61)
        self.assertAlmostEqual(self.rate(at(2026, 7, 16, 16, 0), critical), 26.92)

    def test_weekend_is_off_peak(self) -> None:
        # 2026-07-18 is a Saturday.
        self.assertAlmostEqual(self.rate(at(2026, 7, 18, 16, 0)), 14.31)

    def test_designated_holidays(self) -> None:
        holidays = tariff.designated_holidays(2026)
        self.assertIn(date(2026, 1, 1), holidays)  # New Year's Day
        self.assertIn(date(2026, 4, 3), holidays)  # Good Friday
        self.assertIn(date(2026, 5, 25), holidays)  # Memorial Day
        self.assertIn(date(2026, 7, 4), holidays)  # Independence Day
        self.assertIn(date(2026, 9, 7), holidays)  # Labor Day
        self.assertIn(date(2026, 11, 26), holidays)  # Thanksgiving
        self.assertIn(date(2026, 12, 25), holidays)  # Christmas

        # Thanksgiving is a Thursday; 4-7 p.m. is off-peak.
        self.assertAlmostEqual(self.rate(at(2026, 11, 26, 16, 0)), 14.31)

    def test_plan_switch_on_2026_12_09(self) -> None:
        # Tuesday 2026-12-08 is still Dynamic Peak Pricing.
        self.assertAlmostEqual(self.rate(at(2026, 12, 8, 16, 0)), 26.92)
        self.assertAlmostEqual(self.rate(at(2026, 12, 8, 12, 0)), 18.61)

        # Wednesday 2026-12-09 uses Time of Day, non-summer peak.
        self.assertAlmostEqual(self.rate(at(2026, 12, 9, 16, 0)), 20.05)
        self.assertAlmostEqual(self.rate(at(2026, 12, 9, 14, 59)), 18.44)
        self.assertAlmostEqual(self.rate(at(2026, 12, 9, 19, 0)), 18.44)

    def test_time_of_day_seasons(self) -> None:
        self.assertAlmostEqual(self.rate(at(2027, 6, 15, 16, 0)), 24.13)
        self.assertAlmostEqual(self.rate(at(2027, 5, 28, 16, 0)), 20.05)
        self.assertAlmostEqual(self.rate(at(2027, 9, 30, 16, 0)), 24.13)
        self.assertAlmostEqual(self.rate(at(2027, 10, 1, 16, 0)), 20.05)

    def test_time_of_day_holiday_is_off_peak(self) -> None:
        # Christmas 2026 is a Friday.
        self.assertAlmostEqual(self.rate(at(2026, 12, 25, 16, 0)), 18.44)

    def test_next_change(self) -> None:
        self.assertEqual(
            tariff.next_change(at(2026, 12, 9, 14, 30)),
            at(2026, 12, 9, 15, 0),
        )
        self.assertEqual(
            tariff.next_change(at(2026, 12, 9, 20, 0)),
            at(2026, 12, 10, 15, 0),
        )
        # Dynamic Peak Pricing: mid-peak ends at 23:00.
        self.assertEqual(
            tariff.next_change(at(2026, 12, 8, 20, 0)),
            at(2026, 12, 8, 23, 0),
        )

    def test_parse_critical_dates(self) -> None:
        self.assertEqual(
            tariff.parse_critical_dates("2026-07-15, 2026-08-12"),
            frozenset({date(2026, 7, 15), date(2026, 8, 12)}),
        )
        self.assertEqual(tariff.parse_critical_dates(""), frozenset())
        with self.assertRaises(ValueError):
            tariff.parse_critical_dates("not-a-date")

    def test_every_band_has_a_label(self) -> None:
        self.assertEqual(
            set(const.BAND_LABELS), {band.value for band in tariff.Band}
        )


if __name__ == "__main__":
    unittest.main()
