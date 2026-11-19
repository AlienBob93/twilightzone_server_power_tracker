"""DTE Electric time-of-day tariffs.

Everything here works on local time in DTE's service territory
(``America/Detroit``). Rates are base energy rates in US cents per kWh, as
published by DTE; Power Supply Cost Recovery (PSCR), taxes and program fees are
not included.

Two effective-dated plans are modelled:

``dpp``
    Dynamic Peak Pricing, in effect through 2026-12-08. Three bands plus a
    critical-event override:

    * off-peak  23:00-07:00            14.31 c/kWh
    * mid-peak  07:00-15:00, 19:00-23:00  18.61 c/kWh
    * peak      15:00-19:00            26.92 c/kWh
    * critical  15:00-19:00 on event days  105.00 c/kWh

``tod``
    Time of Day 3 p.m. - 7 p.m., in effect from 2026-12-09. Two bands, with a
    seasonal peak price:

    * off-peak  all other hours        18.44 c/kWh
    * peak      15:00-19:00, Jun 1-Sep 30   24.13 c/kWh
    * peak      15:00-19:00, Oct 1-May 31   20.05 c/kWh

Weekends and DTE's designated holidays are off-peak under both plans.
"""

from __future__ import annotations

from dataclasses import dataclass
from datetime import date, datetime, timedelta
from enum import StrEnum
from functools import lru_cache
from zoneinfo import ZoneInfo

TZ = ZoneInfo("America/Detroit")

# First local day billed under Time of Day 3 p.m. - 7 p.m.
SWITCH_DATE = date(2026, 12, 9)

PLAN_DPP = "dpp"
PLAN_TOD = "tod"


class Band(StrEnum):
    """A rate period."""

    OFF_PEAK = "off_peak"
    MID_PEAK = "mid_peak"
    PEAK = "peak"
    CRITICAL = "critical"


# Dynamic Peak Pricing, cents per kWh.
DPP_OFF_PEAK = 14.31
DPP_MID_PEAK = 18.61
DPP_PEAK = 26.92
DPP_CRITICAL = 105.00

# Time of Day 3 p.m. - 7 p.m., cents per kWh.
TOD_OFF_PEAK = 18.44
TOD_PEAK_SUMMER = 24.13
TOD_PEAK_NON_SUMMER = 20.05


@dataclass(frozen=True, slots=True)
class Rate:
    """A resolved rate for one instant."""

    plan: str
    band: Band
    cents_per_kwh: float

    @property
    def usd_per_kwh(self) -> float:
        """Return the rate in dollars per kWh."""
        return self.cents_per_kwh / 100


def plan_for(day: date) -> str:
    """Return the plan in effect on a local date."""
    return PLAN_TOD if day >= SWITCH_DATE else PLAN_DPP


def is_summer(day: date) -> bool:
    """Return True for the summer peak season (Jun 1 - Sep 30)."""
    return (6, 1) <= (day.month, day.day) <= (9, 30)


def is_weekend(day: date) -> bool:
    """Return True for Saturday and Sunday."""
    return day.weekday() >= 5


@lru_cache(maxsize=None)
def designated_holidays(year: int) -> frozenset[date]:
    """Return DTE's designated holidays for a year.

    DTE lists New Year's Day, Good Friday, Memorial Day, Independence Day,
    Labor Day, Thanksgiving Day and Christmas Day. Weekday holidays are billed
    as off-peak; holidays that fall on a weekend are already off-peak.
    """
    return frozenset(
        {
            date(year, 1, 1),
            _easter(year) - timedelta(days=2),  # Good Friday
            _last_weekday(year, 5, 0),  # Memorial Day: last Monday in May
            date(year, 7, 4),
            _nth_weekday(year, 9, 0, 1),  # Labor Day: first Monday in September
            _nth_weekday(year, 11, 3, 4),  # Thanksgiving: fourth Thursday
            date(year, 12, 25),
        }
    )


def is_designated_holiday(day: date) -> bool:
    """Return True when a date is one of DTE's designated holidays."""
    return day in designated_holidays(day.year)


def rate_at(when: datetime, critical_dates: frozenset[date] = frozenset()) -> Rate:
    """Return the plan, band and rate for a local, timezone-aware datetime."""
    if when.tzinfo is None:
        raise ValueError("rate_at requires a timezone-aware datetime")

    local = when.astimezone(TZ)
    day = local.date()
    plan = plan_for(day)

    if is_weekend(day) or is_designated_holiday(day):
        return Rate(plan, Band.OFF_PEAK, TOD_OFF_PEAK if plan == PLAN_TOD else DPP_OFF_PEAK)

    hour = local.hour

    if plan == PLAN_TOD:
        if 15 <= hour < 19:
            return Rate(plan, Band.PEAK, TOD_PEAK_SUMMER if is_summer(day) else TOD_PEAK_NON_SUMMER)
        return Rate(plan, Band.OFF_PEAK, TOD_OFF_PEAK)

    if 15 <= hour < 19:
        if day in critical_dates:
            return Rate(plan, Band.CRITICAL, DPP_CRITICAL)
        return Rate(plan, Band.PEAK, DPP_PEAK)
    if 7 <= hour < 15 or 19 <= hour < 23:
        return Rate(plan, Band.MID_PEAK, DPP_MID_PEAK)
    return Rate(plan, Band.OFF_PEAK, DPP_OFF_PEAK)


def price_cents_at(when: datetime, critical_dates: frozenset[date] = frozenset()) -> float:
    """Return the rate in cents per kWh for a local, timezone-aware datetime."""
    return rate_at(when, critical_dates).cents_per_kwh


def next_change(when: datetime, critical_dates: frozenset[date] = frozenset()) -> datetime:
    """Return the next instant at which the rate changes.

    Tariff boundaries are on the hour, so stepping hour by hour is exact and
    cheap. The search horizon (400 days) comfortably covers a plan or season
    transition.
    """
    local = when.astimezone(TZ)
    current = price_cents_at(local, critical_dates)
    cursor = local.replace(minute=0, second=0, microsecond=0)

    for _ in range(24 * 400):
        cursor += timedelta(hours=1)
        if price_cents_at(cursor, critical_dates) != current:
            return cursor

    return cursor


def parse_critical_dates(raw: str | None) -> frozenset[date]:
    """Parse a comma-separated list of ISO dates from the options flow."""
    if not raw:
        return frozenset()

    dates: set[date] = set()
    for chunk in raw.split(","):
        chunk = chunk.strip()
        if not chunk:
            continue
        dates.add(date.fromisoformat(chunk))
    return frozenset(dates)


def _easter(year: int) -> date:
    """Return Easter Sunday (anonymous Gregorian algorithm)."""
    a = year % 19
    b, c = divmod(year, 100)
    d, e = divmod(b, 4)
    f = (b + 8) // 25
    g = (b - f + 1) // 3
    h = (19 * a + b - d - g + 15) % 30
    i, k = divmod(c, 4)
    l = (32 + 2 * e + 2 * i - h - k) % 7
    m = (a + 11 * h + 22 * l) // 451
    month = (h + l - 7 * m + 114) // 31
    day = (h + l - 7 * m + 114) % 31 + 1
    return date(year, month, day)


def _nth_weekday(year: int, month: int, weekday: int, n: int) -> date:
    """Return the nth given weekday (Monday=0) of a month."""
    first = date(year, month, 1)
    offset = (weekday - first.weekday()) % 7
    return first + timedelta(days=offset + 7 * (n - 1))


def _last_weekday(year: int, month: int, weekday: int) -> date:
    """Return the last given weekday (Monday=0) of a month."""
    if month == 12:
        last = date(year, 12, 31)
    else:
        last = date(year, month + 1, 1) - timedelta(days=1)
    return last - timedelta(days=(last.weekday() - weekday) % 7)
