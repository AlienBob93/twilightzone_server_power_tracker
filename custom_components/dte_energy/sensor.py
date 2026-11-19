"""Sensors for the DTE Energy usage integration."""

from __future__ import annotations

from typing import Any

from homeassistant.components.sensor import (
    SensorDeviceClass,
    SensorEntity,
    SensorStateClass,
)
from homeassistant.config_entries import ConfigEntry
from homeassistant.const import EntityCategory
from homeassistant.core import callback
from homeassistant.helpers.device_registry import DeviceInfo
from homeassistant.helpers.entity_platform import AddEntitiesCallback
from homeassistant.helpers.event import async_track_time_change
from homeassistant.helpers.update_coordinator import CoordinatorEntity
from homeassistant.util import dt as dt_util

from .const import DOMAIN
from .coordinator import DteCoordinator
from .tariff import Band, TZ, next_change, rate_at


async def async_setup_entry(
    hass,
    entry: ConfigEntry,
    async_add_entities: AddEntitiesCallback,
) -> None:
    """Set up the DTE sensors."""
    coordinator: DteCoordinator = entry.runtime_data
    async_add_entities(
        [
            DtePriceSensor(coordinator, entry),
            DteBandSensor(coordinator, entry),
            DteNextChangeSensor(coordinator, entry),
            DteLastReadingSensor(coordinator, entry),
            DteLastImportSensor(coordinator, entry),
        ]
    )


class DteTimeSensor(SensorEntity):
    """Base for sensors derived from the current time, not the network."""

    _attr_has_entity_name = True
    _attr_should_poll = False

    def __init__(
        self, coordinator: DteCoordinator, entry: ConfigEntry, key: str
    ) -> None:
        self.coordinator = coordinator
        self._attr_unique_id = f"{entry.entry_id}_{key}"
        self._attr_device_info = _device_info(entry)

    async def async_added_to_hass(self) -> None:
        """Refresh on every hour boundary, when the tariff can change."""
        self.async_on_remove(
            async_track_time_change(
                self.hass, self._handle_tick, minute=0, second=0
            )
        )

    @callback
    def _handle_tick(self, _now) -> None:
        self.async_write_ha_state()

    @property
    def available(self) -> bool:
        """The tariff is computed locally, so keep the sensor available."""
        return True


class DtePriceSensor(DteTimeSensor):
    """The current electric rate, in USD per kWh."""

    _attr_translation_key = "current_price"
    _attr_native_unit_of_measurement = "USD/kWh"
    _attr_state_class = SensorStateClass.MEASUREMENT
    _attr_suggested_display_precision = 4
    _attr_icon = "mdi:currency-usd"

    def __init__(self, coordinator: DteCoordinator, entry: ConfigEntry) -> None:
        super().__init__(coordinator, entry, "current_price")

    @property
    def native_value(self) -> float:
        """Return the current rate in USD per kWh."""
        rate = rate_at(dt_util.now(TZ), self.coordinator.critical_dates)
        return round(rate.usd_per_kwh, 5)

    @property
    def extra_state_attributes(self) -> dict[str, Any]:
        """Expose the plan, band and the cents-per-kWh value."""
        rate = rate_at(dt_util.now(TZ), self.coordinator.critical_dates)
        return {
            "plan": rate.plan,
            "band": rate.band.value,
            "cents_per_kwh": rate.cents_per_kwh,
        }


class DteBandSensor(DteTimeSensor):
    """The current rate period."""

    _attr_translation_key = "current_band"
    _attr_device_class = SensorDeviceClass.ENUM
    _attr_options = [band.value for band in Band]
    _attr_icon = "mdi:clock-time-four-outline"

    def __init__(self, coordinator: DteCoordinator, entry: ConfigEntry) -> None:
        super().__init__(coordinator, entry, "current_band")

    @property
    def native_value(self) -> str:
        """Return the current band."""
        return rate_at(dt_util.now(TZ), self.coordinator.critical_dates).band.value


class DteNextChangeSensor(DteTimeSensor):
    """When the rate will next change."""

    _attr_translation_key = "next_rate_change"
    _attr_device_class = SensorDeviceClass.TIMESTAMP
    _attr_icon = "mdi:clock-alert-outline"

    def __init__(self, coordinator: DteCoordinator, entry: ConfigEntry) -> None:
        super().__init__(coordinator, entry, "next_rate_change")

    @property
    def native_value(self):
        """Return the next tariff boundary."""
        return next_change(dt_util.now(TZ), self.coordinator.critical_dates)


class DteLastReadingSensor(CoordinatorEntity[DteCoordinator], SensorEntity):
    """The start of the newest interval in the DTE feed."""

    _attr_has_entity_name = True
    _attr_translation_key = "last_reading"
    _attr_device_class = SensorDeviceClass.TIMESTAMP
    _attr_entity_category = EntityCategory.DIAGNOSTIC
    _attr_icon = "mdi:calendar-clock"

    def __init__(self, coordinator: DteCoordinator, entry: ConfigEntry) -> None:
        super().__init__(coordinator)
        self._attr_unique_id = f"{entry.entry_id}_last_reading"
        self._attr_device_info = _device_info(entry)

    @property
    def native_value(self):
        """Return the newest reading timestamp."""
        return self.coordinator.data.last_reading if self.coordinator.data else None


class DteLastImportSensor(CoordinatorEntity[DteCoordinator], SensorEntity):
    """When statistics were last imported."""

    _attr_has_entity_name = True
    _attr_translation_key = "last_import"
    _attr_device_class = SensorDeviceClass.TIMESTAMP
    _attr_entity_category = EntityCategory.DIAGNOSTIC
    _attr_icon = "mdi:database-clock-outline"

    def __init__(self, coordinator: DteCoordinator, entry: ConfigEntry) -> None:
        super().__init__(coordinator)
        self._attr_unique_id = f"{entry.entry_id}_last_import"
        self._attr_device_info = _device_info(entry)

    @property
    def native_value(self):
        """Return the last successful import timestamp."""
        return self.coordinator.last_import


def _device_info(entry: ConfigEntry) -> DeviceInfo:
    return DeviceInfo(
        identifiers={(DOMAIN, entry.entry_id)},
        name="DTE Energy",
        manufacturer="DTE Energy",
        model="Green Button usage feed",
    )
