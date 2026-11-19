"""Config and options flow for the DTE Energy usage integration."""

from __future__ import annotations

import asyncio
from typing import Any
from urllib.parse import urlparse

import aiohttp
import voluptuous as vol

from homeassistant.config_entries import ConfigFlow, ConfigEntry, OptionsFlow
from homeassistant.core import HomeAssistant, callback
from homeassistant.helpers.aiohttp_client import async_get_clientsession
from homeassistant.helpers.selector import (
    NumberSelector,
    NumberSelectorConfig,
    NumberSelectorMode,
    TextSelector,
)

from .const import (
    ALLOWED_HOST_SUFFIX,
    CONF_CRITICAL_DATES,
    CONF_SCAN_INTERVAL,
    CONF_URL,
    DEFAULT_SCAN_INTERVAL_HOURS,
    DOMAIN,
)
from .dte import DteParseError, parse_feed
from .tariff import parse_critical_dates
from .util import url_unique_id


class CannotConnect(Exception):
    """Raised when the share link cannot be fetched."""


class InvalidData(Exception):
    """Raised when the share link is not a usable DTE feed."""


async def _async_validate(hass: HomeAssistant, url: str) -> None:
    """Fetch and parse the feed so config errors surface immediately."""
    parsed = urlparse(url)
    if parsed.scheme != "https" or not (parsed.hostname or "").endswith(
        ALLOWED_HOST_SUFFIX
    ):
        raise InvalidData

    try:
        async with asyncio.timeout(60):
            session = async_get_clientsession(hass)
            async with session.get(url) as response:
                if response.status != 200:
                    raise CannotConnect
                body = await response.read()
    except (TimeoutError, aiohttp.ClientError) as err:
        raise CannotConnect from err

    try:
        feed = await hass.async_add_executor_job(parse_feed, body)
    except DteParseError as err:
        raise InvalidData from err

    if not feed.electric and not feed.gas:
        raise InvalidData


def _url_schema(default_url: str = "") -> vol.Schema:
    return vol.Schema(
        {
            vol.Required(CONF_URL, default=default_url): TextSelector(),
        }
    )


def _user_schema(defaults: dict[str, Any] | None = None) -> vol.Schema:
    defaults = defaults or {}
    return vol.Schema(
        {
            vol.Required(CONF_URL, default=defaults.get(CONF_URL, "")): TextSelector(),
            vol.Optional(
                CONF_SCAN_INTERVAL,
                default=defaults.get(
                    CONF_SCAN_INTERVAL, DEFAULT_SCAN_INTERVAL_HOURS
                ),
            ): NumberSelector(
                NumberSelectorConfig(
                    min=1,
                    max=24,
                    step=1,
                    mode=NumberSelectorMode.BOX,
                    unit_of_measurement="hours",
                )
            ),
        }
    )


class DteEnergyConfigFlow(ConfigFlow, domain=DOMAIN):
    """Handle the initial setup."""

    VERSION = 1

    async def async_step_user(
        self, user_input: dict[str, Any] | None = None
    ) -> dict[str, Any]:
        """Ask for the DTE share link."""
        if self._async_current_entries():
            return self.async_abort(reason="single_instance_allowed")

        errors: dict[str, str] = {}

        if user_input is not None:
            url = user_input[CONF_URL].strip()
            try:
                await _async_validate(self.hass, url)
            except CannotConnect:
                errors["base"] = "cannot_connect"
            except InvalidData:
                errors["base"] = "invalid_data"
            else:
                await self.async_set_unique_id(url_unique_id(url))
                self._abort_if_unique_id_configured()
                return self.async_create_entry(
                    title="DTE Energy",
                    data={
                        CONF_URL: url,
                        CONF_SCAN_INTERVAL: int(
                            user_input.get(
                                CONF_SCAN_INTERVAL, DEFAULT_SCAN_INTERVAL_HOURS
                            )
                        ),
                    },
                )

        return self.async_show_form(
            step_id="user",
            data_schema=_user_schema(user_input),
            errors=errors,
        )

    async def async_step_reconfigure(
        self, user_input: dict[str, Any] | None = None
    ) -> dict[str, Any]:
        """Update the share link without recreating the config entry."""
        entry = self._get_reconfigure_entry()
        errors: dict[str, str] = {}

        if user_input is not None:
            url = user_input[CONF_URL].strip()
            try:
                await _async_validate(self.hass, url)
            except CannotConnect:
                errors["base"] = "cannot_connect"
            except InvalidData:
                errors["base"] = "invalid_data"
            else:
                return self.async_update_reload_and_abort(
                    entry,
                    data_updates={CONF_URL: url},
                )

        return self.async_show_form(
            step_id="reconfigure",
            data_schema=_url_schema(entry.data.get(CONF_URL, "")),
            errors=errors,
        )

    @staticmethod
    @callback
    def async_get_options_flow(config_entry: ConfigEntry) -> OptionsFlow:
        """Return the options flow."""
        return DteEnergyOptionsFlow()


class DteEnergyOptionsFlow(OptionsFlow):
    """Tune the scan interval and critical-event dates."""

    async def async_step_init(
        self, user_input: dict[str, Any] | None = None
    ) -> dict[str, Any]:
        """Manage the integration options."""
        errors: dict[str, str] = {}

        if user_input is not None:
            try:
                parse_critical_dates(user_input.get(CONF_CRITICAL_DATES, ""))
            except ValueError:
                errors[CONF_CRITICAL_DATES] = "invalid_critical_dates"
            else:
                return self.async_create_entry(
                    data={
                        CONF_SCAN_INTERVAL: int(user_input[CONF_SCAN_INTERVAL]),
                        CONF_CRITICAL_DATES: user_input.get(
                            CONF_CRITICAL_DATES, ""
                        ).strip(),
                    }
                )

        options = self.config_entry.options
        defaults = {
            CONF_SCAN_INTERVAL: options.get(
                CONF_SCAN_INTERVAL,
                self.config_entry.data.get(
                    CONF_SCAN_INTERVAL, DEFAULT_SCAN_INTERVAL_HOURS
                ),
            ),
            CONF_CRITICAL_DATES: options.get(CONF_CRITICAL_DATES, ""),
        }

        return self.async_show_form(
            step_id="init",
            data_schema=vol.Schema(
                {
                    vol.Optional(
                        CONF_SCAN_INTERVAL,
                        default=defaults[CONF_SCAN_INTERVAL],
                    ): NumberSelector(
                        NumberSelectorConfig(
                            min=1,
                            max=24,
                            step=1,
                            mode=NumberSelectorMode.BOX,
                            unit_of_measurement="hours",
                        )
                    ),
                    vol.Optional(
                        CONF_CRITICAL_DATES,
                        default=defaults[CONF_CRITICAL_DATES],
                    ): TextSelector(),
                }
            ),
            errors=errors,
            description_placeholders={
                "critical_hint": "YYYY-MM-DD, YYYY-MM-DD"
            },
        )
