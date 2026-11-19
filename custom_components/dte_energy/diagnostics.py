"""Diagnostics for the DTE Energy usage integration."""

from __future__ import annotations

from typing import Any

from homeassistant.config_entries import ConfigEntry
from homeassistant.core import HomeAssistant

from .const import (
    CONF_CRITICAL_DATES,
    CONF_SCAN_INTERVAL,
    DEFAULT_SCAN_INTERVAL_HOURS,
)


async def async_get_config_entry_diagnostics(
    hass: HomeAssistant, entry: ConfigEntry
) -> dict[str, Any]:
    """Return redacted diagnostics.

    The share link is a secret: it grants read access to the household's usage
    history. It is replaced with a fixed marker.
    """
    coordinator = getattr(entry, "runtime_data", None)
    data = coordinator.data if coordinator else None

    return {
        "share_link": "**REDACTED**",
        "scan_interval_hours": int(
            entry.options.get(
                CONF_SCAN_INTERVAL,
                entry.data.get(CONF_SCAN_INTERVAL, DEFAULT_SCAN_INTERVAL_HOURS),
            )
        ),
        "critical_dates": entry.options.get(CONF_CRITICAL_DATES, ""),
        "has_electric": coordinator.has_electric if coordinator else None,
        "last_import": (
            coordinator.last_import.isoformat()
            if coordinator and coordinator.last_import
            else None
        ),
        "last_reading": (
            data.last_reading.isoformat() if data and data.last_reading else None
        ),
        "import_error": data.import_error if data else None,
    }
