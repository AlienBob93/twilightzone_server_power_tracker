package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"twilightzone/ups-mqtt/internal/config"
	"twilightzone/ups-mqtt/internal/pwrstat"
)

// statusOutput is representative pwrstat -status output. "Rating Power" is
// included on purpose: it also ends in "Watt" and must never be read as the
// load, which is what makes this an end-to-end check of parser + formatter.
const statusOutput = `The UPS information shows as following:

	Properties:
		Model Name................... EC850LCD
		Rating Power................. 510 Watt

	Current UPS status:
		State........................ Normal
		Load......................... 66 Watt(13 %)
`

const zeroLoadOutput = `	Current UPS status:
		Load......................... 0 Watt(0 %)
`

const fractionalLoadOutput = `	Current UPS status:
		Load......................... 66.5 Watt(13 %)
`

// writeFakePwrstat writes a stand-in for the vendor CLI and returns its path.
// readLoad resolves the path directly, so a temp directory is enough.
func writeFakePwrstat(t *testing.T, body string) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("these tests rely on a POSIX shell")
	}

	path := filepath.Join(t.TempDir(), "pwrstat")
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"+body), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

// scriptEchoing returns a shell body that prints output verbatim.
func scriptEchoing(output string) string {
	return "cat <<'EOF'\n" + output + "EOF\n"
}

// fakeCLIConfig points a Config at a fake pwrstat binary.
func fakeCLIConfig(path string) config.Config {
	return config.Config{
		PwrstatPath:    path,
		PwrstatTimeout: 5 * time.Second,
	}
}

func TestReadLoad(t *testing.T) {
	tests := []struct {
		name   string
		output string
		want   string
	}{
		{name: "integer watts", output: statusOutput, want: "66"},
		// A zero reading must produce "0", not an empty string: main treats an
		// empty payload as "the probe failed" and skips the state publish.
		{name: "zero load still yields a payload", output: zeroLoadOutput, want: "0"},
		{name: "fractional watts are preserved", output: fractionalLoadOutput, want: "66.5"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			cfg := fakeCLIConfig(writeFakePwrstat(t, scriptEchoing(test.output)))

			got, err := readLoad(context.Background(), cfg)
			if err != nil {
				t.Fatalf("readLoad() unexpected error: %v", err)
			}
			if got != test.want {
				t.Fatalf("readLoad() = %q, want %q", got, test.want)
			}
		})
	}
}

// TestReadLoadParseErrorIsRetryable pins the classification main relies on:
// output drift must not trigger log.Fatalf, and no payload may be published.
func TestReadLoadParseErrorIsRetryable(t *testing.T) {
	cfg := fakeCLIConfig(writeFakePwrstat(t, scriptEchoing("\t\tState........................ Normal\n")))

	got, err := readLoad(context.Background(), cfg)
	if !errors.Is(err, pwrstat.ErrFieldMissing) {
		t.Fatalf("error = %v, want ErrFieldMissing", err)
	}
	if got != "" {
		t.Fatalf("payload = %q, want empty on error", got)
	}
	if pwrstat.IsUnusable(err) {
		t.Fatalf("output drift must stay retryable, got %v", err)
	}
}

// TestReadLoadDaemonFailureIsRetryable covers the case main counts towards
// offlineThreshold: the binary ran, printed a diagnostic and exited non-zero.
func TestReadLoadDaemonFailureIsRetryable(t *testing.T) {
	cfg := fakeCLIConfig(writeFakePwrstat(t, "echo 'pwrstatd is not running' >&2\nexit 1\n"))

	got, err := readLoad(context.Background(), cfg)
	if err == nil {
		t.Fatal("expected an error")
	}
	if got != "" {
		t.Fatalf("payload = %q, want empty on error", got)
	}
	if pwrstat.IsUnusable(err) {
		t.Fatalf("a stopped daemon must stay retryable, got %v", err)
	}
}

// TestReadLoadMissingBinaryIsUnusable guards the one path that exits the
// process: a missing binary will never fix itself, so main calls log.Fatalf.
func TestReadLoadMissingBinaryIsUnusable(t *testing.T) {
	cfg := fakeCLIConfig(filepath.Join(t.TempDir(), "absent"))

	got, err := readLoad(context.Background(), cfg)
	if err == nil {
		t.Fatal("expected an error")
	}
	if got != "" {
		t.Fatalf("payload = %q, want empty on error", got)
	}
	if !pwrstat.IsUnusable(err) {
		t.Fatalf("want IsUnusable, got %v", err)
	}
}
