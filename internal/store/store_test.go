package store

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"twilightzone/ups-monitor/internal/model"
)

func ptr[T any](value T) *T { return &value }

func newTestStore(t *testing.T) *Store {
	t.Helper()

	st, err := Open(context.Background(), filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() {
		if err := st.Close(); err != nil {
			t.Errorf("Close: %v", err)
		}
	})
	return st
}

func TestInsertAndLatest(t *testing.T) {
	st := newTestStore(t)
	now := time.Now().UTC().Truncate(time.Millisecond)

	sample := model.Sample{
		Timestamp:      now,
		UPS:            "ec850lcd",
		Status:         "OL CHRG",
		InputVoltage:   ptr(230.1),
		OutputVoltage:  ptr(229.8),
		LoadPercent:    ptr(21.0),
		BatteryCharge:  ptr(100.0),
		BatteryRuntime: ptr(int64(1800)),
		BatteryVoltage: ptr(13.6),
		Temperature:    ptr(28.5),
		NominalPower:   ptr(510.0),
		EstimatedPower: ptr(107.1),
		OnBattery:      false,
		LowBattery:     false,
		Raw:            map[string]string{"ups.model": "EC850LCD"},
	}

	id, err := st.InsertSample(context.Background(), sample)
	if err != nil {
		t.Fatalf("InsertSample: %v", err)
	}
	if id == 0 {
		t.Fatal("expected a non-zero row ID")
	}

	got, err := st.LatestSample(context.Background())
	if err != nil {
		t.Fatalf("LatestSample: %v", err)
	}
	if got.ID != id {
		t.Errorf("ID = %d, want %d", got.ID, id)
	}
	if got.Timestamp.UnixMilli() != now.UnixMilli() {
		t.Errorf("timestamp = %s, want %s", got.Timestamp, now)
	}
	if got.UPS != "ec850lcd" || got.Status != "OL CHRG" {
		t.Errorf("identity = %q / %q", got.UPS, got.Status)
	}
	if got.InputVoltage == nil || *got.InputVoltage != 230.1 {
		t.Errorf("input voltage = %v", got.InputVoltage)
	}
	if got.OutputVoltage == nil || *got.OutputVoltage != 229.8 {
		t.Errorf("output voltage = %v", got.OutputVoltage)
	}
	if got.BatteryRuntime == nil || *got.BatteryRuntime != 1800 {
		t.Errorf("battery runtime = %v", got.BatteryRuntime)
	}
	if got.Raw["ups.model"] != "EC850LCD" {
		t.Errorf("raw = %v", got.Raw)
	}
}

func TestLatestNotFound(t *testing.T) {
	st := newTestStore(t)

	_, err := st.LatestSample(context.Background())
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
}

func TestSamplesWindowOrder(t *testing.T) {
	st := newTestStore(t)
	base := time.Now().UTC().Truncate(time.Millisecond).Add(-10 * time.Minute)

	for i := 0; i < 3; i++ {
		sample := model.Sample{
			Timestamp:    base.Add(time.Duration(i) * time.Minute),
			UPS:          "test",
			InputVoltage: ptr(230.0 + float64(i)),
		}
		if _, err := st.InsertSample(context.Background(), sample); err != nil {
			t.Fatalf("InsertSample: %v", err)
		}
	}

	got, err := st.Samples(context.Background(), base, base.Add(90*time.Second), 10)
	if err != nil {
		t.Fatalf("Samples: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d samples, want 2", len(got))
	}
	if !got[0].Timestamp.Before(got[1].Timestamp) {
		t.Errorf("samples are not chronological: %s, %s", got[0].Timestamp, got[1].Timestamp)
	}
	if got[0].InputVoltage == nil || *got[0].InputVoltage != 230.0 {
		t.Errorf("first sample = %+v", got[0])
	}
}

func TestStats(t *testing.T) {
	st := newTestStore(t)
	base := time.Now().UTC().Truncate(time.Millisecond).Add(-time.Hour)

	samples := []model.Sample{
		{
			Timestamp:     base,
			UPS:           "test",
			Status:        "OL",
			InputVoltage:  ptr(230.0),
			LoadPercent:   ptr(10.0),
			BatteryCharge: ptr(100.0),
		},
		{
			Timestamp:     base.Add(10 * time.Minute),
			UPS:           "test",
			Status:        "OB LB",
			InputVoltage:  ptr(220.0),
			LoadPercent:   ptr(30.0),
			BatteryCharge: ptr(40.0),
			OnBattery:     true,
			LowBattery:    true,
		},
	}
	for _, sample := range samples {
		if _, err := st.InsertSample(context.Background(), sample); err != nil {
			t.Fatalf("InsertSample: %v", err)
		}
	}

	stats, err := st.Stats(context.Background(), base.Add(-time.Minute), base.Add(time.Hour))
	if err != nil {
		t.Fatalf("Stats: %v", err)
	}
	if stats.Samples != 2 {
		t.Errorf("samples = %d", stats.Samples)
	}
	if stats.OnBatterySamples != 1 || stats.LowBatterySamples != 1 {
		t.Errorf("battery counts = %d / %d", stats.OnBatterySamples, stats.LowBatterySamples)
	}
	if stats.From == nil || stats.To == nil {
		t.Fatalf("expected from/to bounds")
	}

	voltage := stats.Metrics["input_voltage"]
	if voltage.Min == nil || voltage.Avg == nil || voltage.Max == nil {
		t.Fatalf("input_voltage metric = %+v", voltage)
	}
	if *voltage.Min != 220.0 || *voltage.Max != 230.0 || *voltage.Avg != 225.0 {
		t.Errorf("input_voltage = min %v avg %v max %v", *voltage.Min, *voltage.Avg, *voltage.Max)
	}

	load := stats.Metrics["load_percent"]
	if load.Avg == nil || *load.Avg != 20.0 {
		t.Errorf("load avg = %v", load.Avg)
	}
}

func TestPrune(t *testing.T) {
	st := newTestStore(t)
	now := time.Now().UTC().Truncate(time.Millisecond)

	old := model.Sample{Timestamp: now.Add(-2 * time.Hour), UPS: "test"}
	recent := model.Sample{Timestamp: now.Add(-10 * time.Minute), UPS: "test"}
	for _, sample := range []model.Sample{old, recent} {
		if _, err := st.InsertSample(context.Background(), sample); err != nil {
			t.Fatalf("InsertSample: %v", err)
		}
	}

	removed, err := st.Prune(context.Background(), now.Add(-time.Hour))
	if err != nil {
		t.Fatalf("Prune: %v", err)
	}
	if removed != 1 {
		t.Errorf("removed = %d, want 1", removed)
	}

	latest, err := st.LatestSample(context.Background())
	if err != nil {
		t.Fatalf("LatestSample: %v", err)
	}
	if latest.Timestamp.UnixMilli() != recent.Timestamp.UnixMilli() {
		t.Errorf("latest = %s, want %s", latest.Timestamp, recent.Timestamp)
	}
}

func TestOpenCreatesDirectory(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "dir", "ups.db")
	st, err := Open(context.Background(), path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer st.Close()

	if err := st.Ping(context.Background()); err != nil {
		t.Fatalf("Ping: %v", err)
	}
}
