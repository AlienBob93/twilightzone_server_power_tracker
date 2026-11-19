"""Tests for the DTE Gas residential rate table (stdlib only)."""

from __future__ import annotations

from datetime import date
import importlib.util
from pathlib import Path
import sys
import unittest

GAS_PATH = (
    Path(__file__).resolve().parents[1]
    / "custom_components"
    / "dte_energy"
    / "gas_tariff.py"
)
_spec = importlib.util.spec_from_file_location("dte_gas_tariff", GAS_PATH)
assert _spec and _spec.loader
gas = importlib.util.module_from_spec(_spec)
sys.modules[_spec.name] = gas
_spec.loader.exec_module(gas)


class GasTariffTest(unittest.TestCase):
    """Monthly Rate A rates, fallbacks and the fixed charge."""

    def test_november_2026(self) -> None:
        rate = gas.rate_for(date(2026, 11, 15))
        self.assertAlmostEqual(rate.fixed_usd, 15.40)
        self.assertAlmostEqual(rate.usd_per_ccf, 0.95012)

    def test_january_2026_includes_irm(self) -> None:
        rate = gas.rate_for(date(2026, 1, 15))
        self.assertAlmostEqual(rate.fixed_usd, 14.50 + 2.26)
        self.assertAlmostEqual(rate.usd_per_ccf, 0.90644)

    def test_before_the_card_uses_the_first_month(self) -> None:
        rate = gas.rate_for(date(2025, 9, 20))
        self.assertAlmostEqual(rate.fixed_usd, 14.50 + 0.66)
        self.assertAlmostEqual(rate.usd_per_ccf, 0.89837)

    def test_after_the_card_uses_the_last_month(self) -> None:
        rate = gas.rate_for(date(2027, 6, 1))
        self.assertAlmostEqual(rate.fixed_usd, 15.40)
        self.assertAlmostEqual(rate.usd_per_ccf, 0.95012)

    def test_daily_cost_without_fixed_charge(self) -> None:
        # 2 Ccf at the November 2026 rate, plus 4% Michigan sales tax.
        cost = gas.cost_for_day(date(2026, 11, 2), 2.0, False)
        self.assertAlmostEqual(cost, 2.0 * 0.95012 * 1.04)

    def test_daily_cost_with_fixed_charge(self) -> None:
        cost = gas.cost_for_day(date(2026, 11, 1), 2.0, True)
        self.assertAlmostEqual(cost, (2.0 * 0.95012 + 15.40) * 1.04)


if __name__ == "__main__":
    unittest.main()
