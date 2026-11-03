package pwrstat

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// statusEC850LCD is verbatim output from the target UPS (CyberPower EC850LCD).
// Fields are indented with tabs, and the dot padding is aligned to the longest
// label in this particular output.
const statusEC850LCD = `The UPS information shows as following:

	Properties:
		Model Name................... EC850LCD
		Firmware Number..............
		Rating Voltage............... 120 V
		Rating Power................. 510 Watt

	Current UPS status:
		State........................ Normal
		Power Supply by.............. Utility Power
		Utility Voltage.............. 121 V
		Output Voltage............... 121 V
		Battery Capacity............. 92 %
		Remaining Runtime............ 45 min.
		Load......................... 66 Watt(13 %)
		Test Result.................. Unknown
		Last Power Event............. None
`

// statusCP585 is the example from the vendor manual for a different model. Its
// dot padding deliberately differs from statusEC850LCD: the parser must not
// depend on the column width. Note that "Rating Power" carries a "(335 Watt)"
// parenthetical that must never be mistaken for the load.
const statusCP585 = `	Properties:
		Model Name.................... UPS CP585
		Firmware Number............... BFH8102-6O1.5
		Rating Voltage................ 120 V
		Rating Power.................. 515 VA (335 Watt)

	Current UPS status:
		State......................... Normal
		Power Supply by............... Utility Power
		Utility Voltage............... 111 V
		Output Voltage................ 110 V
		Battery Capacity.............. 100 %
		Remaining Runtime............. 60 min.
		Load.......................... 0 Watt(0 %)
		Test Result................... Passed at 2022/03/15 09:35:35
		Last Power Event.............. Blackout at 2022/03/14 09:03:32 for 2 min
`

// TestParseLoad covers the parsing contract: successful readings (including a
// legitimate zero) and the two error classes that callers branch on with
// errors.Is. The fixtures are real output, so the table fails if a format
// assumption drifts.
func TestParseLoad(t *testing.T) {
	tests := []struct {
		name    string
		output  string
		want    float64
		wantErr error // nil means success; otherwise errors.Is must match
	}{
		{name: "ec850lcd normal", output: statusEC850LCD, want: 66},
		{name: "cp585 manual example is zero, not the rating", output: statusCP585, want: 0},
		{name: "minimal dot padding", output: "Load....66 Watt(13 %)\n", want: 66},
		{name: "spaces for indentation", output: "        Load......................... 66 Watt(13 %)\n", want: 66},
		{name: "fractional watts", output: "\t\tLoad......................... 66.5 Watt(13 %)\n", want: 66.5},
		{name: "lowercase unit", output: "\t\tLoad......................... 66 watt(13 %)\n", want: 66},
		{name: "zero load", output: "\t\tLoad......................... 0 Watt(0 %)\n", want: 0},

		{name: "no load field", output: "\t\tState........................ Normal\n", wantErr: ErrFieldMissing},
		{name: "empty output", output: "", wantErr: ErrFieldMissing},
		{name: "only rating power present", output: "\t\tRating Power................. 510 Watt\n", wantErr: ErrFieldMissing},
		{name: "another label containing Load", output: "\t\tOverload..................... 66 Watt(13 %)\n", wantErr: ErrFieldMissing},
		{name: "no dot leader", output: "        Load        66 Watt(13 %)\n", wantErr: ErrFieldMissing},

		{name: "load field with empty value", output: "\t\tLoad.........................\n", wantErr: ErrValueUnparseable},
		{name: "load without unit", output: "\t\tLoad......................... 66\n", wantErr: ErrValueUnparseable},
		{name: "unparsable value", output: "\t\tLoad......................... unknown\n", wantErr: ErrValueUnparseable},
		{name: "number is not the leading token", output: "\t\tLoad......................... 3 x 66 Watt(5 %)\n", wantErr: ErrValueUnparseable},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := ParseLoad(test.output)

			if test.wantErr != nil {
				if !errors.Is(err, test.wantErr) {
					t.Fatalf("error = %v, want %v", err, test.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != test.want {
				t.Fatalf("ParseLoad() = %v, want %v", got, test.want)
			}
		})
	}
}

// TestParseLoadNeverUsesRatingPower guards the specific trap in statusCP585:
// a "(335 Watt)" parenthetical sitting in a different field.
func TestParseLoadNeverUsesRatingPower(t *testing.T) {
	got, err := ParseLoad(statusCP585)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != 0 {
		t.Fatalf("ParseLoad() = %v, want 0: the Rating Power value must not be used", got)
	}
}

// TestParseLoadErrorCarriesTheValue makes sure the unparseable-value error says
// what the daemon actually printed, which is the only clue in a container log.
func TestParseLoadErrorCarriesTheValue(t *testing.T) {
	_, err := ParseLoad("\t\tLoad......................... unknown\n")
	if err == nil {
		t.Fatal("expected an error")
	}
	if !strings.Contains(err.Error(), "unknown") {
		t.Fatalf("error %q does not mention the offending value", err)
	}
}

// TestSplitField pins the split rule ParseLoad depends on: the first run of two
// or more dots separates label from value, a line without one is not a field at
// all, and a field may legitimately carry an empty value.
func TestSplitField(t *testing.T) {
	tests := []struct {
		name      string
		line      string
		wantLabel string
		wantValue string
		wantOK    bool
	}{
		{
			name:      "load line",
			line:      "\t\tLoad......................... 66 Watt(13 %)",
			wantLabel: "Load",
			wantValue: "66 Watt(13 %)",
			wantOK:    true,
		},
		{
			name:      "minimum dot run",
			line:      "Load....66 Watt(13 %)",
			wantLabel: "Load",
			wantValue: "66 Watt(13 %)",
			wantOK:    true,
		},
		{
			name:      "valueless field",
			line:      "\t\tFirmware Number..............",
			wantLabel: "Firmware Number",
			wantValue: "",
			wantOK:    true,
		},
		{
			name:   "no dot run",
			line:   "\tCurrent UPS status:",
			wantOK: false,
		},
		{
			name:   "empty line",
			line:   "",
			wantOK: false,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			label, value, ok := splitField(test.line)
			if ok != test.wantOK {
				t.Fatalf("ok = %v, want %v", ok, test.wantOK)
			}
			if !ok {
				return
			}
			if label != test.wantLabel || value != test.wantValue {
				t.Fatalf("splitField() = (%q, %q), want (%q, %q)", label, value, test.wantLabel, test.wantValue)
			}
		})
	}
}

// writePwrstat writes a stand-in for the vendor CLI and returns its path.
// Collect resolves the path directly instead of searching PATH, so a temp
// directory is enough.
func writePwrstat(t *testing.T, body string) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("collector tests rely on a POSIX shell")
	}

	path := filepath.Join(t.TempDir(), "pwrstat")
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"+body), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

// TestCollect exercises the happy path through the real exec layer, so the
// locale pinning, the stderr buffer and the hand-off to ParseLoad are covered
// together rather than mocked away.
func TestCollect(t *testing.T) {
	path := writePwrstat(t, "cat <<'EOF'\n"+statusEC850LCD+"EOF\n")

	got, err := Collect(context.Background(), path, 5*time.Second)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != 66 {
		t.Fatalf("Collect() = %v, want 66", got)
	}
}

// TestCollectDaemonDown covers the class main must retry on: the binary ran,
// printed a diagnostic and exited non-zero.
func TestCollectDaemonDown(t *testing.T) {
	path := writePwrstat(t, "echo 'pwrstatd is not running' >&2\nexit 1\n")

	_, err := Collect(context.Background(), path, 5*time.Second)
	if err == nil {
		t.Fatal("expected an error")
	}
	if IsUnusable(err) {
		t.Fatalf("a stopped daemon must stay retryable, got %v", err)
	}
	if !strings.Contains(err.Error(), "pwrstatd is not running") {
		t.Fatalf("error %q does not include the daemon's stderr", err)
	}
}

// TestCollectPropagatesParseErrors makes sure the sentinels survive the exec
// layer, since main classifies with errors.Is.
func TestCollectPropagatesParseErrors(t *testing.T) {
	path := writePwrstat(t, "echo 'State........................ Normal'\n")

	_, err := Collect(context.Background(), path, 5*time.Second)
	if !errors.Is(err, ErrFieldMissing) {
		t.Fatalf("error = %v, want ErrFieldMissing", err)
	}
	if IsUnusable(err) {
		t.Fatalf("output drift must stay retryable, got %v", err)
	}
}

// TestCollectMissingBinary covers one of the two misconfiguration classes: a
// path that does not exist must be reported as unusable so startup can refuse
// to run instead of retrying forever.
func TestCollectMissingBinary(t *testing.T) {
	_, err := Collect(context.Background(), filepath.Join(t.TempDir(), "absent"), time.Second)
	if err == nil {
		t.Fatal("expected an error")
	}
	if !IsUnusable(err) {
		t.Fatalf("want IsUnusable, got %v", err)
	}
}

// TestCollectNonExecutableBinary covers the other misconfiguration class: the
// file exists but cannot be executed, which is what a wrong service user looks
// like. Root bypasses the mode check, so the test is skipped there.
func TestCollectNonExecutableBinary(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root can execute a file regardless of its mode")
	}

	path := filepath.Join(t.TempDir(), "pwrstat")
	if err := os.WriteFile(path, []byte("#!/bin/sh\nexit 0\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	_, err := Collect(context.Background(), path, time.Second)
	if err == nil {
		t.Fatal("expected an error")
	}
	if !IsUnusable(err) {
		t.Fatalf("want IsUnusable, got %v", err)
	}
}

// TestCollectTimeout checks both halves of the deadline: the error is classified
// as context.DeadlineExceeded rather than as an unusable binary, and the call
// returns promptly instead of waiting for the child to finish on its own.
func TestCollectTimeout(t *testing.T) {
	// exec so the shell is replaced by sleep: killing the child then actually
	// stops it, which keeps the test fast and independent of WaitDelay.
	path := writePwrstat(t, "exec sleep 5\n")

	start := time.Now()
	_, err := Collect(context.Background(), path, 200*time.Millisecond)
	elapsed := time.Since(start)

	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("error = %v, want context.DeadlineExceeded", err)
	}
	if IsUnusable(err) {
		t.Fatalf("a timeout must stay retryable, got %v", err)
	}
	if !strings.Contains(err.Error(), "timed out") {
		t.Fatalf("error %q should mention the timeout", err)
	}
	if elapsed > 2*time.Second {
		t.Fatalf("call took %s with a 200ms timeout", elapsed)
	}
}

// TestCollectCancelled checks that a cancelled parent context reports as
// cancellation, not as a timeout. During shutdown the distinction is the
// difference between "we stopped on purpose" and "the daemon is too slow".
func TestCollectCancelled(t *testing.T) {
	path := writePwrstat(t, "exec sleep 5\n")

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := Collect(ctx, path, 5*time.Second)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v, want context.Canceled", err)
	}
	if IsUnusable(err) {
		t.Fatalf("cancellation must stay retryable, got %v", err)
	}
	if !strings.Contains(err.Error(), "cancelled") {
		t.Fatalf("error %q should mention cancellation", err)
	}
}

// TestCollectSilentFailure covers the final fallback in Collect: the process
// exited non-zero without writing anything to stderr, so there is no daemon
// message to attach. The sample must still stay retryable.
func TestCollectSilentFailure(t *testing.T) {
	path := writePwrstat(t, "exit 1\n")

	_, err := Collect(context.Background(), path, 5*time.Second)
	if err == nil {
		t.Fatal("expected an error")
	}
	if IsUnusable(err) {
		t.Fatalf("a silent non-zero exit must stay retryable, got %v", err)
	}
	if !strings.Contains(err.Error(), "-status") {
		t.Fatalf("error %q should name the command", err)
	}
}
