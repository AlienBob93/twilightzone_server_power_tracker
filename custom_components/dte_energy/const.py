"""Constants for the DTE Energy usage integration."""

from __future__ import annotations

DOMAIN = "dte_energy"

CONF_URL = "url"
CONF_SCAN_INTERVAL = "scan_interval"
CONF_CRITICAL_DATES = "critical_dates"

DEFAULT_SCAN_INTERVAL_HOURS = 6

# Green Button share links are served from this host.
ALLOWED_HOST_SUFFIX = ".dteenergy.com"

# External statistic ids. The colon form marks them as external statistics,
# which is what makes them selectable in the Energy dashboard.
ENERGY_STAT_ID = f"{DOMAIN}:grid_consumption"
COST_STAT_ID = f"{DOMAIN}:grid_cost"

ENERGY_STAT_NAME = "DTE electric grid consumption"
COST_STAT_NAME = "DTE electric grid cost"

GAS_STAT_ID = f"{DOMAIN}:gas_consumption"
GAS_STAT_NAME = "DTE gas consumption"

# Tariff band -> human label used in the per-band statistic names.
BAND_LABELS = {
    "off_peak": "off-peak",
    "mid_peak": "mid-peak",
    "peak": "peak",
    "critical": "critical",
}
