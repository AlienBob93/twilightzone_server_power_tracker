"""Data update coordinator for the DTE Energy usage integration."""

from __future__ import annotations

import asyncio
from dataclasses import dataclass
from datetime import date, datetime, timedelta
import logging

import aiohttp

from homeassistant.config_entries import ConfigEntry
from homeassistant.core import HomeAssistant
from homeassistant.helpers.aiohttp_client import async_get_clientsession
from homeassistant.helpers.update_coordinator import DataUpdateCoordinator, UpdateFailed
from homeassistant.util import dt as dt_util

from .const import DOMAIN
from .dte import DteParseError, ElectricInterval, parse_feed
from .statistics import ImportResult, async_import_gas, async_import_intervals

_LOGGER = logging.getLogger(__name__)

FETCH_TIMEOUT = 60


@dataclass(slots=True)
class DteData:
    """Data produced by one refresh."""

    intervals: list[ElectricInterval]
    last_reading: datetime | None
    import_result: ImportResult | None
    import_error: str | None


class DteCoordinator(DataUpdateCoordinator[DteData]):
    """Fetch the DTE share link and import its statistics."""

    def __init__(
        self,
        hass: HomeAssistant,
        entry: ConfigEntry,
        url: str,
        scan_interval_hours: int,
        critical_dates: frozenset[date],
    ) -> None:
        super().__init__(
            hass,
            _LOGGER,
            name=DOMAIN,
            update_interval=timedelta(hours=scan_interval_hours),
            config_entry=entry,
        )
        self.url = url
        self.entry = entry
        self.critical_dates = critical_dates
        self.last_import: datetime | None = None

    async def _async_update_data(self) -> DteData:
        try:
            body = await self._async_fetch()
        except (TimeoutError, aiohttp.ClientError, OSError) as err:
            raise UpdateFailed(f"could not fetch DTE share link: {err}") from err

        try:
            feed = await self.hass.async_add_executor_job(parse_feed, body)
        except DteParseError as err:
            raise UpdateFailed(str(err)) from err

        if not feed.electric:
            raise UpdateFailed("the feed has no electric interval readings")

        import_result: ImportResult | None = None
        errors: list[str] = []

        try:
            import_result = await async_import_intervals(
                self.hass, self.entry.entry_id, feed.electric, self.critical_dates
            )
        except Exception:  # noqa: BLE001 - keep the price sensors alive
            _LOGGER.exception("Could not import DTE electric statistics")
            errors.append("electric statistics import failed")

        try:
            await async_import_gas(self.hass, self.entry.entry_id, feed.gas)
        except Exception:  # noqa: BLE001 - gas is independent of the electric import
            _LOGGER.exception("Could not import DTE gas statistics")
            errors.append("gas statistics import failed")

        if not errors:
            self.last_import = dt_util.utcnow()

        last_reading = feed.electric[-1].start
        return DteData(
            intervals=feed.electric,
            last_reading=last_reading,
            import_result=import_result,
            import_error="; ".join(errors) or None,
        )

    async def _async_fetch(self) -> bytes:
        session = async_get_clientsession(self.hass)
        async with asyncio.timeout(FETCH_TIMEOUT):
            async with session.get(self.url) as response:
                if response.status != 200:
                    raise UpdateFailed(
                        f"DTE returned HTTP {response.status}"
                    )
                return await response.read()
