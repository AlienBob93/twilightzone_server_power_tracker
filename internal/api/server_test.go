package api

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"twilightzone/ups-monitor/internal/model"
	"twilightzone/ups-monitor/internal/store"
)

type stubStatus struct {
	status model.CollectorStatus
}

func (s stubStatus) Status() model.CollectorStatus { return s.status }

func newTestServer(t *testing.T) *httptest.Server {
	t.Helper()

	st, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "api.db"))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { st.Close() })

	inputVoltage := 230.0
	load := 20.0
	charge := 100.0
	estimatedPower := 102.0

	_, err = st.InsertSample(context.Background(), model.Sample{
		Timestamp:      time.Now().UTC().Truncate(time.Millisecond),
		UPS:            "ec850lcd",
		Status:         "OL CHRG",
		InputVoltage:   &inputVoltage,
		LoadPercent:    &load,
		BatteryCharge:  &charge,
		EstimatedPower: &estimatedPower,
		Raw:            map[string]string{"ups.model": "EC850LCD"},
	})
	if err != nil {
		t.Fatalf("InsertSample: %v", err)
	}

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	server, err := New(st, stubStatus{status: model.CollectorStatus{UPS: "ec850lcd"}}, nil, time.Minute, 10, logger)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	ts := httptest.NewServer(server.Handler())
	t.Cleanup(ts.Close)
	return ts
}

func get(t *testing.T, url string) *http.Response {
	t.Helper()
	response, err := http.Get(url)
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}
	return response
}

func TestHealth(t *testing.T) {
	ts := newTestServer(t)

	response := get(t, ts.URL+"/api/health")
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", response.StatusCode)
	}

	var payload map[string]string
	if err := json.NewDecoder(response.Body).Decode(&payload); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if payload["status"] != "ok" || payload["database"] != "ok" {
		t.Errorf("payload = %v", payload)
	}
}

func TestStatus(t *testing.T) {
	ts := newTestServer(t)

	response := get(t, ts.URL+"/api/status")
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", response.StatusCode)
	}

	var payload struct {
		Sample    *model.Sample          `json:"sample"`
		Stale     bool                   `json:"stale"`
		Collector *model.CollectorStatus `json:"collector"`
	}
	if err := json.NewDecoder(response.Body).Decode(&payload); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if payload.Sample == nil {
		t.Fatal("expected a sample")
	}
	if payload.Sample.UPS != "ec850lcd" {
		t.Errorf("ups = %q", payload.Sample.UPS)
	}
	if payload.Stale {
		t.Error("fresh sample reported as stale")
	}
	if payload.Collector == nil || payload.Collector.UPS != "ec850lcd" {
		t.Errorf("collector = %+v", payload.Collector)
	}
}

func TestSamples(t *testing.T) {
	ts := newTestServer(t)

	response := get(t, ts.URL+"/api/samples?hours=1")
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", response.StatusCode)
	}

	var payload struct {
		Samples []model.Sample `json:"samples"`
		Count   int            `json:"count"`
	}
	if err := json.NewDecoder(response.Body).Decode(&payload); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if payload.Count != 1 || len(payload.Samples) != 1 {
		t.Fatalf("count = %d, samples = %d", payload.Count, len(payload.Samples))
	}
	if payload.Samples[0].InputVoltage == nil || *payload.Samples[0].InputVoltage != 230.0 {
		t.Errorf("input voltage = %v", payload.Samples[0].InputVoltage)
	}
}

func TestStats(t *testing.T) {
	ts := newTestServer(t)

	response := get(t, ts.URL+"/api/stats?hours=1")
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", response.StatusCode)
	}

	var payload struct {
		Stats store.Stats `json:"stats"`
	}
	if err := json.NewDecoder(response.Body).Decode(&payload); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if payload.Stats.Samples != 1 {
		t.Errorf("samples = %d", payload.Stats.Samples)
	}
	voltage := payload.Stats.Metrics["input_voltage"]
	if voltage.Avg == nil || *voltage.Avg != 230.0 {
		t.Errorf("input voltage avg = %v", voltage.Avg)
	}
}

func TestDashboard(t *testing.T) {
	ts := newTestServer(t)

	response := get(t, ts.URL+"/")
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", response.StatusCode)
	}
	if contentType := response.Header.Get("Content-Type"); !strings.Contains(contentType, "text/html") {
		t.Errorf("content type = %q", contentType)
	}

	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	for _, want := range []string{"UPS Monitor", "voltageChart", "/static/app.js"} {
		if !strings.Contains(string(body), want) {
			t.Errorf("dashboard is missing %q", want)
		}
	}
}

func TestBadRequests(t *testing.T) {
	ts := newTestServer(t)

	for _, path := range []string{
		"/api/samples?hours=nope",
		"/api/samples?hours=0",
		"/api/samples?bogus=1",
		"/api/samples?limit=0",
		"/api/stats?since=not-a-time",
	} {
		response := get(t, ts.URL+path)
		response.Body.Close()
		if response.StatusCode != http.StatusBadRequest {
			t.Errorf("%s: status = %d, want 400", path, response.StatusCode)
		}
	}
}

func TestUPSUnavailable(t *testing.T) {
	ts := newTestServer(t)

	response := get(t, ts.URL+"/api/ups")
	defer response.Body.Close()
	if response.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", response.StatusCode)
	}
}

func TestStaticAsset(t *testing.T) {
	ts := newTestServer(t)

	response := get(t, ts.URL+"/static/style.css")
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", response.StatusCode)
	}
	if contentType := response.Header.Get("Content-Type"); !strings.Contains(contentType, "text/css") {
		t.Errorf("content type = %q", contentType)
	}
}
