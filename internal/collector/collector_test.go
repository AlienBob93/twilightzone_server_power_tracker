package collector

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"log/slog"
	"math"
	"net"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"twilightzone/ups-monitor/internal/nut"
	"twilightzone/ups-monitor/internal/store"
)

func TestBuildSample(t *testing.T) {
	vars := map[string]string{
		"ups.status":            "OB DISCHRG LB",
		"input.voltage":         "229.9",
		"output.voltage":        "229.5",
		"input.frequency":       "50.0",
		"output.frequency":      "50.0",
		"ups.load":              "20",
		"battery.charge":        "85",
		"battery.runtime":       "900",
		"battery.voltage":       "13.1",
		"ups.temperature":       "31.5",
		"ups.realpower.nominal": "510",
		"ups.model":             "EC850LCD",
		"driver.version":        "2.8.2",
	}

	sample := buildSample("test", vars, time.Unix(0, 0).UTC())

	if !sample.OnBattery || !sample.LowBattery {
		t.Errorf("battery flags = %v / %v", sample.OnBattery, sample.LowBattery)
	}
	if sample.InputVoltage == nil || *sample.InputVoltage != 229.9 {
		t.Errorf("input voltage = %v", sample.InputVoltage)
	}
	if sample.BatteryCharge == nil || *sample.BatteryCharge != 85 {
		t.Errorf("battery charge = %v", sample.BatteryCharge)
	}
	if sample.BatteryRuntime == nil || *sample.BatteryRuntime != 900 {
		t.Errorf("battery runtime = %v", sample.BatteryRuntime)
	}
	if sample.EstimatedPower == nil || math.Abs(*sample.EstimatedPower-102.0) > 1e-9 {
		t.Errorf("estimated power = %v", sample.EstimatedPower)
	}
	if sample.Raw["ups.model"] != "EC850LCD" {
		t.Errorf("raw model = %q", sample.Raw["ups.model"])
	}
	if _, ok := sample.Raw["driver.version"]; ok {
		t.Errorf("driver metadata should be filtered out: %v", sample.Raw)
	}
}

func TestBuildSampleHandlesMissingValues(t *testing.T) {
	sample := buildSample("test", map[string]string{
		"ups.status":      "OL",
		"input.voltage":   "N/A",
		"battery.runtime": "",
	}, time.Unix(0, 0).UTC())

	if sample.InputVoltage != nil || sample.BatteryRuntime != nil || sample.EstimatedPower != nil {
		t.Errorf("expected nil optional values, got %+v", sample)
	}
	if sample.OnBattery || sample.LowBattery {
		t.Errorf("unexpected battery flags")
	}
}

func TestCollectStoresSample(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { listener.Close() })

	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			go func(conn net.Conn) {
				defer conn.Close()
				reader := bufio.NewReader(conn)
				for {
					line, err := reader.ReadString('\n')
					if err != nil {
						return
					}
					if strings.TrimSpace(line) != "LIST VAR test" {
						fmt.Fprint(conn, "ERR UNKNOWN-COMMAND\n")
						continue
					}
					fmt.Fprint(conn, "VAR test ups.status \"OL CHRG\"\n")
					fmt.Fprint(conn, "VAR test input.voltage \"230.0\"\n")
					fmt.Fprint(conn, "VAR test ups.load \"20\"\n")
					fmt.Fprint(conn, "VAR test battery.charge \"100\"\n")
					fmt.Fprint(conn, "VAR test ups.realpower.nominal \"510\"\n")
					fmt.Fprint(conn, "END LIST VAR test\n")
				}
			}(conn)
		}
	}()

	host, portString, err := net.SplitHostPort(listener.Addr().String())
	if err != nil {
		t.Fatalf("split address: %v", err)
	}
	port, err := strconv.Atoi(portString)
	if err != nil {
		t.Fatalf("parse port: %v", err)
	}

	st, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "collector.db"))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { st.Close() })

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	client := nut.New(host, port, "", "", 2*time.Second)
	instance := New(client, st, 10*time.Second, "test", 0, logger)

	sample, err := instance.Collect(context.Background())
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	if sample.Status != "OL CHRG" {
		t.Errorf("status = %q", sample.Status)
	}

	stored, err := st.LatestSample(context.Background())
	if err != nil {
		t.Fatalf("LatestSample: %v", err)
	}
	if stored.InputVoltage == nil || *stored.InputVoltage != 230.0 {
		t.Errorf("stored input voltage = %v", stored.InputVoltage)
	}
	if stored.EstimatedPower == nil || math.Abs(*stored.EstimatedPower-102.0) > 1e-9 {
		t.Errorf("stored estimated power = %v", stored.EstimatedPower)
	}
	if stored.UPS != "test" {
		t.Errorf("stored UPS = %q", stored.UPS)
	}
}
