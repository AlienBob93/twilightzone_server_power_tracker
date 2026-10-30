// Package model contains the data types shared by the collector, storage and
// HTTP layers.
package model

import (
	"strings"
	"time"
)

// Sample is one point-in-time reading from a UPS.
type Sample struct {
	ID              int64             `json:"id"`
	Timestamp       time.Time         `json:"timestamp"`
	UPS             string            `json:"ups"`
	Status          string            `json:"status"`
	InputVoltage    *float64          `json:"input_voltage,omitempty"`
	OutputVoltage   *float64          `json:"output_voltage,omitempty"`
	InputFrequency  *float64          `json:"input_frequency,omitempty"`
	OutputFrequency *float64          `json:"output_frequency,omitempty"`
	LoadPercent     *float64          `json:"load_percent,omitempty"`
	BatteryCharge   *float64          `json:"battery_charge,omitempty"`
	BatteryRuntime  *int64            `json:"battery_runtime_seconds,omitempty"`
	BatteryVoltage  *float64          `json:"battery_voltage,omitempty"`
	Temperature     *float64          `json:"temperature_c,omitempty"`
	NominalPower    *float64          `json:"nominal_power_w,omitempty"`
	EstimatedPower  *float64          `json:"estimated_power_w,omitempty"`
	OnBattery       bool              `json:"on_battery"`
	LowBattery      bool              `json:"low_battery"`
	Raw             map[string]string `json:"raw,omitempty"`
}

// ParseStatus splits NUT's ups.status flags into the booleans the dashboard
// cares about. upsd returns space-separated tokens such as "OL CHRG" or
// "OB DISCHRG LB".
func ParseStatus(status string) (onBattery, lowBattery bool) {
	for _, flag := range strings.Fields(status) {
		switch flag {
		case "OB":
			onBattery = true
		case "LB":
			lowBattery = true
		}
	}
	return onBattery, lowBattery
}

// CollectorStatus is a snapshot of the collector's health. It is served by the
// status API so the dashboard can distinguish an idle UPS from a broken NUT
// connection.
type CollectorStatus struct {
	LastSuccess *time.Time `json:"last_success,omitempty"`
	LastError   string     `json:"last_error,omitempty"`
	LastErrorAt *time.Time `json:"last_error_at,omitempty"`
	UPS         string     `json:"ups,omitempty"`
}
