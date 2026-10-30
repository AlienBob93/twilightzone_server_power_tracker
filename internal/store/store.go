// Package store persists UPS samples in SQLite.
package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"twilightzone/ups-monitor/internal/model"

	_ "modernc.org/sqlite" // pure-Go SQLite driver (registers "sqlite")
)

// ErrNotFound is returned when a requested row does not exist.
var ErrNotFound = errors.New("store: not found")

// Store wraps the SQLite database.
type Store struct {
	db *sql.DB
}

var schema = []string{
	`CREATE TABLE IF NOT EXISTS samples (
		id               INTEGER PRIMARY KEY AUTOINCREMENT,
		ts               INTEGER NOT NULL,
		ups              TEXT    NOT NULL,
		status           TEXT    NOT NULL DEFAULT '',
		input_voltage    REAL,
		output_voltage   REAL,
		input_frequency  REAL,
		output_frequency REAL,
		load_percent     REAL,
		battery_charge   REAL,
		battery_runtime  INTEGER,
		battery_voltage  REAL,
		temperature      REAL,
		nominal_power    REAL,
		estimated_power  REAL,
		on_battery       INTEGER NOT NULL DEFAULT 0,
		low_battery      INTEGER NOT NULL DEFAULT 0,
		raw              TEXT    NOT NULL DEFAULT '{}'
	)`,
	`CREATE INDEX IF NOT EXISTS idx_samples_ts ON samples (ts)`,
	`CREATE INDEX IF NOT EXISTS idx_samples_ups_ts ON samples (ups, ts)`,
}

// Open opens (and creates, if needed) the SQLite database at path.
func Open(ctx context.Context, path string) (*Store, error) {
	if strings.TrimSpace(path) == "" {
		return nil, errors.New("store: empty database path")
	}

	if path != ":memory:" && !strings.HasPrefix(path, "file:") {
		if dir := filepath.Dir(path); dir != "" && dir != "." {
			if err := os.MkdirAll(dir, 0o755); err != nil {
				return nil, fmt.Errorf("store: create database directory: %w", err)
			}
		}
	}

	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("store: open %s: %w", path, err)
	}

	// SQLite is happiest with a single writer. The application is small enough
	// that serializing access is cheaper than managing a lock hierarchy.
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)

	if err := db.PingContext(ctx); err != nil {
		db.Close()
		return nil, fmt.Errorf("store: ping %s: %w", path, err)
	}

	for _, pragma := range []string{
		"PRAGMA journal_mode = WAL",
		"PRAGMA synchronous = NORMAL",
		"PRAGMA busy_timeout = 5000",
		"PRAGMA foreign_keys = ON",
	} {
		if _, err := db.ExecContext(ctx, pragma); err != nil {
			db.Close()
			return nil, fmt.Errorf("store: apply %q: %w", pragma, err)
		}
	}

	store := &Store{db: db}
	if err := store.migrate(ctx); err != nil {
		db.Close()
		return nil, err
	}
	return store, nil
}

func (s *Store) migrate(ctx context.Context) error {
	for i, statement := range schema {
		if _, err := s.db.ExecContext(ctx, statement); err != nil {
			return fmt.Errorf("store: migration %d: %w", i+1, err)
		}
	}
	return nil
}

// Close closes the underlying database.
func (s *Store) Close() error { return s.db.Close() }

// Ping verifies that the database is reachable.
func (s *Store) Ping(ctx context.Context) error { return s.db.PingContext(ctx) }

// InsertSample stores a sample and returns its row ID.
func (s *Store) InsertSample(ctx context.Context, sample model.Sample) (int64, error) {
	raw := sample.Raw
	if raw == nil {
		raw = map[string]string{}
	}
	rawJSON, err := json.Marshal(raw)
	if err != nil {
		return 0, fmt.Errorf("store: marshal raw variables: %w", err)
	}

	result, err := s.db.ExecContext(ctx, `
		INSERT INTO samples (
			ts, ups, status, input_voltage, output_voltage, input_frequency,
			output_frequency, load_percent, battery_charge, battery_runtime,
			battery_voltage, temperature, nominal_power, estimated_power,
			on_battery, low_battery, raw
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		sample.Timestamp.UnixMilli(),
		sample.UPS,
		sample.Status,
		floatValue(sample.InputVoltage),
		floatValue(sample.OutputVoltage),
		floatValue(sample.InputFrequency),
		floatValue(sample.OutputFrequency),
		floatValue(sample.LoadPercent),
		floatValue(sample.BatteryCharge),
		intValue(sample.BatteryRuntime),
		floatValue(sample.BatteryVoltage),
		floatValue(sample.Temperature),
		floatValue(sample.NominalPower),
		floatValue(sample.EstimatedPower),
		boolValue(sample.OnBattery),
		boolValue(sample.LowBattery),
		string(rawJSON),
	)
	if err != nil {
		return 0, fmt.Errorf("store: insert sample: %w", err)
	}

	id, err := result.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("store: insert sample id: %w", err)
	}
	return id, nil
}

const sampleColumns = `
	id, ts, ups, status, input_voltage, output_voltage, input_frequency,
	output_frequency, load_percent, battery_charge, battery_runtime,
	battery_voltage, temperature, nominal_power, estimated_power,
	on_battery, low_battery, raw`

// LatestSample returns the most recent sample.
func (s *Store) LatestSample(ctx context.Context) (model.Sample, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT `+sampleColumns+` FROM samples ORDER BY ts DESC, id DESC LIMIT 1`)

	sample, err := scanSample(row)
	if errors.Is(err, sql.ErrNoRows) {
		return model.Sample{}, ErrNotFound
	}
	if err != nil {
		return model.Sample{}, err
	}
	return sample, nil
}

// Samples returns samples in the inclusive [from, to] window in chronological
// order. At most limit rows are returned, keeping the newest ones.
func (s *Store) Samples(ctx context.Context, from, to time.Time, limit int) ([]model.Sample, error) {
	if limit <= 0 {
		limit = 1000
	}

	rows, err := s.db.QueryContext(ctx, `
		SELECT `+sampleColumns+`
		FROM samples
		WHERE ts >= ? AND ts <= ?
		ORDER BY ts DESC, id DESC
		LIMIT ?`,
		from.UnixMilli(), to.UnixMilli(), limit)
	if err != nil {
		return nil, fmt.Errorf("store: query samples: %w", err)
	}
	defer rows.Close()

	samples := make([]model.Sample, 0, min(limit, 1024))
	for rows.Next() {
		sample, err := scanSample(rows)
		if err != nil {
			return nil, err
		}
		samples = append(samples, sample)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: iterate samples: %w", err)
	}

	// The query returns newest first; charts want oldest first.
	for i, j := 0, len(samples)-1; i < j; i, j = i+1, j-1 {
		samples[i], samples[j] = samples[j], samples[i]
	}
	return samples, nil
}

// MetricStats holds min/avg/max for one metric.
type MetricStats struct {
	Min *float64 `json:"min,omitempty"`
	Avg *float64 `json:"avg,omitempty"`
	Max *float64 `json:"max,omitempty"`
}

// Stats summarises a time window.
type Stats struct {
	Samples           int64                  `json:"samples"`
	From              *time.Time             `json:"from,omitempty"`
	To                *time.Time             `json:"to,omitempty"`
	OnBatterySamples  int64                  `json:"on_battery_samples"`
	LowBatterySamples int64                  `json:"low_battery_samples"`
	Metrics           map[string]MetricStats `json:"metrics"`
}

const summaryQuery = `
	SELECT COUNT(*),
	       COALESCE(SUM(CASE WHEN on_battery = 1 THEN 1 ELSE 0 END), 0),
	       COALESCE(SUM(CASE WHEN low_battery = 1 THEN 1 ELSE 0 END), 0),
	       MIN(ts), MAX(ts)
	FROM samples
	WHERE ts >= ? AND ts <= ?`

const metricsQuery = `
	WITH filtered AS (
		SELECT * FROM samples WHERE ts >= ? AND ts <= ?
	)
	SELECT 'input_voltage', MIN(input_voltage), AVG(input_voltage), MAX(input_voltage) FROM filtered
	UNION ALL SELECT 'output_voltage', MIN(output_voltage), AVG(output_voltage), MAX(output_voltage) FROM filtered
	UNION ALL SELECT 'input_frequency', MIN(input_frequency), AVG(input_frequency), MAX(input_frequency) FROM filtered
	UNION ALL SELECT 'output_frequency', MIN(output_frequency), AVG(output_frequency), MAX(output_frequency) FROM filtered
	UNION ALL SELECT 'load_percent', MIN(load_percent), AVG(load_percent), MAX(load_percent) FROM filtered
	UNION ALL SELECT 'battery_charge', MIN(battery_charge), AVG(battery_charge), MAX(battery_charge) FROM filtered
	UNION ALL SELECT 'battery_runtime_seconds', MIN(battery_runtime), AVG(battery_runtime), MAX(battery_runtime) FROM filtered
	UNION ALL SELECT 'battery_voltage', MIN(battery_voltage), AVG(battery_voltage), MAX(battery_voltage) FROM filtered
	UNION ALL SELECT 'temperature_c', MIN(temperature), AVG(temperature), MAX(temperature) FROM filtered
	UNION ALL SELECT 'estimated_power_w', MIN(estimated_power), AVG(estimated_power), MAX(estimated_power) FROM filtered`

// Stats computes aggregate values for the inclusive [from, to] window.
func (s *Store) Stats(ctx context.Context, from, to time.Time) (Stats, error) {
	stats := Stats{Metrics: make(map[string]MetricStats)}

	var (
		minTS, maxTS          sql.NullInt64
		onBattery, lowBattery int64
	)
	err := s.db.QueryRowContext(ctx, summaryQuery, from.UnixMilli(), to.UnixMilli()).
		Scan(&stats.Samples, &onBattery, &lowBattery, &minTS, &maxTS)
	if err != nil {
		return stats, fmt.Errorf("store: query summary: %w", err)
	}
	stats.OnBatterySamples = onBattery
	stats.LowBatterySamples = lowBattery
	if minTS.Valid {
		ts := time.UnixMilli(minTS.Int64).UTC()
		stats.From = &ts
	}
	if maxTS.Valid {
		ts := time.UnixMilli(maxTS.Int64).UTC()
		stats.To = &ts
	}

	rows, err := s.db.QueryContext(ctx, metricsQuery, from.UnixMilli(), to.UnixMilli())
	if err != nil {
		return stats, fmt.Errorf("store: query metrics: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var (
			name     string
			min, avg sql.NullFloat64
			max      sql.NullFloat64
		)
		if err := rows.Scan(&name, &min, &avg, &max); err != nil {
			return stats, fmt.Errorf("store: scan metric: %w", err)
		}
		stats.Metrics[name] = MetricStats{
			Min: nullableFloat(min),
			Avg: nullableFloat(avg),
			Max: nullableFloat(max),
		}
	}
	if err := rows.Err(); err != nil {
		return stats, fmt.Errorf("store: iterate metrics: %w", err)
	}
	return stats, nil
}

// Prune deletes samples older than cutoff and returns the number removed.
func (s *Store) Prune(ctx context.Context, cutoff time.Time) (int64, error) {
	result, err := s.db.ExecContext(ctx, `DELETE FROM samples WHERE ts < ?`, cutoff.UnixMilli())
	if err != nil {
		return 0, fmt.Errorf("store: prune samples: %w", err)
	}
	removed, err := result.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("store: prune rows affected: %w", err)
	}
	return removed, nil
}

type rowScanner interface {
	Scan(dest ...any) error
}

func scanSample(row rowScanner) (model.Sample, error) {
	var (
		sample                                                    model.Sample
		ts                                                        int64
		inputVoltage, outputVoltage                               sql.NullFloat64
		inputFrequency, outputFrequency                           sql.NullFloat64
		loadPercent, batteryCharge                                sql.NullFloat64
		batteryRuntime                                            sql.NullInt64
		batteryVoltage, temperature, nominalPower, estimatedPower sql.NullFloat64
		onBattery, lowBattery                                     int64
		raw                                                       string
	)

	err := row.Scan(
		&sample.ID,
		&ts,
		&sample.UPS,
		&sample.Status,
		&inputVoltage,
		&outputVoltage,
		&inputFrequency,
		&outputFrequency,
		&loadPercent,
		&batteryCharge,
		&batteryRuntime,
		&batteryVoltage,
		&temperature,
		&nominalPower,
		&estimatedPower,
		&onBattery,
		&lowBattery,
		&raw,
	)
	if err != nil {
		return model.Sample{}, err
	}

	sample.Timestamp = time.UnixMilli(ts).UTC()
	sample.InputVoltage = nullableFloat(inputVoltage)
	sample.OutputVoltage = nullableFloat(outputVoltage)
	sample.InputFrequency = nullableFloat(inputFrequency)
	sample.OutputFrequency = nullableFloat(outputFrequency)
	sample.LoadPercent = nullableFloat(loadPercent)
	sample.BatteryCharge = nullableFloat(batteryCharge)
	sample.BatteryRuntime = nullableInt(batteryRuntime)
	sample.BatteryVoltage = nullableFloat(batteryVoltage)
	sample.Temperature = nullableFloat(temperature)
	sample.NominalPower = nullableFloat(nominalPower)
	sample.EstimatedPower = nullableFloat(estimatedPower)
	sample.OnBattery = onBattery != 0
	sample.LowBattery = lowBattery != 0

	if raw != "" {
		if err := json.Unmarshal([]byte(raw), &sample.Raw); err != nil {
			return model.Sample{}, fmt.Errorf("store: decode raw variables: %w", err)
		}
	}
	return sample, nil
}

func nullableFloat(value sql.NullFloat64) *float64 {
	if !value.Valid {
		return nil
	}
	v := value.Float64
	return &v
}

func nullableInt(value sql.NullInt64) *int64 {
	if !value.Valid {
		return nil
	}
	v := value.Int64
	return &v
}

func floatValue(value *float64) any {
	if value == nil {
		return nil
	}
	return *value
}

func intValue(value *int64) any {
	if value == nil {
		return nil
	}
	return *value
}

func boolValue(value bool) int {
	if value {
		return 1
	}
	return 0
}
