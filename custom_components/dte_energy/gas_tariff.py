"""DTE Gas residential (Rate A / GCR) rates.

The table is taken from DTE's November 2026 rate card:
https://www.dteenergy.com/content/dam/dteenergy/deg/website/common/about-us/company-information/dte-gas-company/notices/rateCard.pdf

The rate card reports a monthly customer charge plus a monthly IRM surcharge,
and a per-Ccf total rate (distribution + GCR + Energy Waste Reduction +
reservation) for each month. Rate A / GCR is the standard residential supply
rate. Rates exclude state sales tax and any city utility users tax.

Months before the first card month use the first card, and months after the
last card month use the last card until the table is updated.

The account is in Washtenaw County: Rate A's 4% Michigan sales tax applies and
there is no City of Detroit utility users tax. The tax is included in the
computed cost; the electric side remains base-rate only.
"""

from __future__ import annotations

from dataclasses import dataclass
from datetime import date
from zoneinfo import ZoneInfo

TZ = ZoneInfo("America/Detroit")

# Michigan sales tax on residential gas (Rate A / 2A) per the rate card.
MICHIGAN_SALES_TAX = 0.04


@dataclass(frozen=True, slots=True)
class GasRate:
    """One month's residential gas rate."""

    effective: date
    fixed_usd: float  # customer charge + monthly IRM surcharge
    usd_per_ccf: float


# Rate A / GCR, from the monthly rate cards in the November 2026 PDF.
GAS_RATES: tuple[GasRate, ...] = (
    GasRate(date(2025, 11, 1), 14.50 + 0.66, 0.89837),
    GasRate(date(2025, 12, 1), 14.50 + 0.66, 0.88637),
    GasRate(date(2026, 1, 1), 14.50 + 2.26, 0.90644),
    GasRate(date(2026, 2, 1), 14.50 + 2.26, 0.88053),
    GasRate(date(2026, 3, 1), 14.50 + 2.26, 0.90653),
    GasRate(date(2026, 4, 1), 14.50 + 2.26, 0.85853),
    GasRate(date(2026, 5, 1), 14.50 + 2.26, 0.85853),
    GasRate(date(2026, 6, 1), 14.50 + 2.26, 0.92153),
    GasRate(date(2026, 7, 1), 14.50 + 2.26, 0.92153),
    GasRate(date(2026, 8, 1), 14.50 + 2.26, 0.92153),
    GasRate(date(2026, 9, 1), 14.50 + 2.26, 0.92153),
    GasRate(date(2026, 10, 1), 15.40, 0.95012),
    GasRate(date(2026, 11, 1), 15.40, 0.95012),
)

# Bump when the table changes.
GAS_TARIFF_VERSION = 1


def rate_for(day: date) -> GasRate:
    """Return the rate card in effect on a local date."""
    chosen = GAS_RATES[0]
    for rate in GAS_RATES:
        if rate.effective <= day:
            chosen = rate
        else:
            break
    return chosen


def cost_for_day(day: date, ccf: float, charge_fixed: bool) -> float:
    """Return the gas cost for one day.

    ``charge_fixed`` is true on the first imported day of each calendar month,
    where the monthly customer charge and IRM are billed. Michigan sales tax is
    added; Washtenaw County has no local utility users tax.
    """
    rate = rate_for(day)
    cost = ccf * rate.usd_per_ccf
    if charge_fixed:
        cost += rate.fixed_usd
    return cost * (1 + MICHIGAN_SALES_TAX)
