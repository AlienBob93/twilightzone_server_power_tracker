// Package collector polls a NUT server and persists samples.
package collector

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"strconv"
	"strings"
	"sync"
	"time"

	"twilightzone/ups-monitor/internal/model"
	"twilightzone/ups-monitor/internal/nut"
	"twilightzone/ups-monitor/internal/store"
)

// Collector periodically reads the UPS variables and writes a sample to the
// store.
type Collector struct {
	client    *nut.Client
	store     *store.Store
	interval  time.Duration
	ups       string
	retention time.Duration
	logger    *slog.Logger

	mu              sync.RWMutex
	status          model.CollectorStatus
	lastLoggedError string
}

// New creates a collector. A blank ups name (or "auto") makes the collector
// discover the first UPS exposed by upsd.
func New(client *nut.Client, st *store.Store, interval time.Duration, ups string, retention time.Duration, logger *slog.Logger) *Collector {
	if interval <= 0 {
		interval = 10 * time.Second
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &Collector{
		client:    client,
		store:     st,
		interval:  interval,
		ups:       strings.TrimSpace(ups),
		retention: retention,
		logger:    logger,
	}
}

// Run collects immediately and then on every interval until ctx is cancelled.
// It also prunes old samples once an hour.
func (c *Collector) Run(ctx context.Context) {
	c.collectAndLog(ctx)
	c.prune(ctx)

	ticker := time.NewTicker(c.interval)
	defer ticker.Stop()

	pruneTicker := time.NewTicker(time.Hour)
	defer pruneTicker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			c.collectAndLog(ctx)
		case <-pruneTicker.C:
			c.prune(ctx)
		}
	}
}

// Collect reads one sample from NUT and stores it.
func (c *Collector) Collect(ctx context.Context) (model.Sample, error) {
	ups := c.ups
	if ups == "" || strings.EqualFold(ups, "auto") {
		list, err := c.client.ListUPS(ctx)
		if err != nil {
			return model.Sample{}, fmt.Errorf("discover UPS: %w", err)
		}
		if len(list) == 0 {
			return model.Sample{}, errors.New("no UPS found on the NUT server")
		}
		ups = list[0].Name
	}

	vars, err := c.client.ListVars(ctx, ups)
	if err != nil {
		return model.Sample{}, fmt.Errorf("read variables for %s: %w", ups, err)
	}

	sample := buildSample(ups, vars, time.Now().UTC())
	if _, err := c.store.InsertSample(ctx, sample); err != nil {
		return sample, fmt.Errorf("store sample: %w", err)
	}
	return sample, nil
}

// Status returns a snapshot of the collector's health.
func (c *Collector) Status() model.CollectorStatus {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.status
}

func (c *Collector) collectAndLog(ctx context.Context) {
	sample, err := c.Collect(ctx)
	now := time.Now().UTC()

	c.mu.Lock()
	if err != nil {
		if ctx.Err() != nil {
			c.mu.Unlock()
			return
		}
		c.status.LastError = err.Error()
		c.status.LastErrorAt = &now
		changed := c.lastLoggedError != err.Error()
		c.lastLoggedError = err.Error()
		c.mu.Unlock()

		if changed {
			c.logger.Warn("collection failed", "error", err)
		} else {
			c.logger.Debug("collection still failing", "error", err)
		}
		return
	}

	recovered := c.lastLoggedError != ""
	c.lastLoggedError = ""
	c.status.LastSuccess = &now
	c.status.UPS = sample.UPS
	c.status.LastError = ""
	c.status.LastErrorAt = nil
	c.mu.Unlock()

	if recovered {
		c.logger.Info("collection recovered", "ups", sample.UPS)
	}
	c.logger.Debug("stored sample", "ups", sample.UPS, "status", sample.Status)
}

func (c *Collector) prune(ctx context.Context) {
	if c.retention <= 0 {
		return
	}
	cutoff := time.Now().UTC().Add(-c.retention)
	removed, err := c.store.Prune(ctx, cutoff)
	if err != nil {
		if ctx.Err() == nil {
			c.logger.Warn("failed to prune old samples", "error", err)
		}
		return
	}
	if removed > 0 {
		c.logger.Info("pruned old samples", "count", removed, "cutoff", cutoff)
	}
}

func buildSample(ups string, vars map[string]string, now time.Time) model.Sample {
	status := strings.TrimSpace(vars["ups.status"])
	onBattery, lowBattery := model.ParseStatus(status)

	sample := model.Sample{
		Timestamp:       now,
		UPS:             ups,
		Status:          status,
		InputVoltage:    floatVar(vars, "input.voltage"),
		OutputVoltage:   floatVar(vars, "output.voltage"),
		InputFrequency:  floatVar(vars, "input.frequency"),
		OutputFrequency: floatVar(vars, "output.frequency"),
		LoadPercent:     floatVar(vars, "ups.load"),
		BatteryCharge:   floatVar(vars, "battery.charge"),
		BatteryRuntime:  intVar(vars, "battery.runtime"),
		BatteryVoltage:  floatVar(vars, "battery.voltage"),
		Temperature:     floatVar(vars, "ups.temperature", "battery.temperature"),
		NominalPower:    floatVar(vars, "ups.realpower.nominal", "ups.power.nominal"),
		OnBattery:       onBattery,
		LowBattery:      lowBattery,
		Raw:             filterVars(vars),
	}

	if sample.LoadPercent != nil && sample.NominalPower != nil {
		power := *sample.LoadPercent / 100 * *sample.NominalPower
		sample.EstimatedPower = &power
	}
	return sample
}

func floatVar(vars map[string]string, names ...string) *float64 {
	for _, name := range names {
		raw, ok := vars[name]
		if !ok {
			continue
		}
		value, err := strconv.ParseFloat(strings.TrimSpace(raw), 64)
		if err != nil {
			continue
		}
		return &value
	}
	return nil
}

func intVar(vars map[string]string, name string) *int64 {
	raw, ok := vars[name]
	if !ok {
		return nil
	}
	value, err := strconv.ParseFloat(strings.TrimSpace(raw), 64)
	if err != nil {
		return nil
	}
	rounded := int64(math.Round(value))
	return &rounded
}

// rawPrefixes keeps the stored raw payload focused on telemetry instead of
// driver/build metadata that rarely changes.
var rawPrefixes = []string{"battery.", "device.", "input.", "output.", "ups."}

func filterVars(vars map[string]string) map[string]string {
	filtered := make(map[string]string, len(vars))
	for name, value := range vars {
		for _, prefix := range rawPrefixes {
			if strings.HasPrefix(name, prefix) {
				filtered[name] = value
				break
			}
		}
	}
	return filtered
}
