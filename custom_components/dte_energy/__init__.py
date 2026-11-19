"""DTE Energy usage -> Home Assistant Energy dashboard."""

from __future__ import annotations

from homeassistant.config_entries import ConfigEntry
from homeassistant.const import Platform
from homeassistant.core import HomeAssistant

from .const import (
    CONF_CRITICAL_DATES,
    CONF_SCAN_INTERVAL,
    CONF_URL,
    DEFAULT_SCAN_INTERVAL_HOURS,
    DOMAIN,
)
from .coordinator import DteCoordinator
from .tariff import parse_critical_dates
from .util import url_unique_id

PLATFORMS: list[Platform] = [Platform.SENSOR]


async def async_setup_entry(hass: HomeAssistant, entry: ConfigEntry) -> bool:
    """Set up DTE Energy usage from a config entry."""
    # Older entries used the secret share token as their unique ID. Replace it
    # with a hash without touching the entry ID, Store or statistics.
    desired_unique_id = url_unique_id(entry.data[CONF_URL])
    if entry.unique_id != desired_unique_id and not any(
        other.entry_id != entry.entry_id and other.unique_id == desired_unique_id
        for other in hass.config_entries.async_entries(DOMAIN)
    ):
        hass.config_entries.async_update_entry(
            entry, unique_id=desired_unique_id
        )

    critical_dates = parse_critical_dates(
        entry.options.get(CONF_CRITICAL_DATES, "")
    )
    scan_interval = int(
        entry.options.get(
            CONF_SCAN_INTERVAL,
            entry.data.get(CONF_SCAN_INTERVAL, DEFAULT_SCAN_INTERVAL_HOURS),
        )
    )

    coordinator = DteCoordinator(
        hass,
        entry,
        entry.data[CONF_URL],
        scan_interval,
        critical_dates,
    )
    # A failed fetch should not take the price sensors down; the coordinator
    # keeps retrying and the diagnostics show the last successful import.
    await coordinator.async_refresh()

    entry.runtime_data = coordinator
    await hass.config_entries.async_forward_entry_setups(entry, PLATFORMS)
    entry.async_on_unload(entry.add_update_listener(_async_reload_entry))
    return True


async def async_unload_entry(hass: HomeAssistant, entry: ConfigEntry) -> bool:
    """Unload a config entry."""
    unloaded = await hass.config_entries.async_unload_platforms(entry, PLATFORMS)
    if unloaded:
        coordinator = getattr(entry, "runtime_data", None)
        if coordinator is not None:
            await coordinator.async_shutdown()
    return unloaded


async def _async_reload_entry(hass: HomeAssistant, entry: ConfigEntry) -> None:
    """Reload when the options change."""
    await hass.config_entries.async_reload(entry.entry_id)
