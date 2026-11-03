// Package pwrstat reads the UPS load from CyberPower's pwrstat status output.
package pwrstat

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"time"
)

var (
	// ErrFieldMissing means the output had no "Load" field at all: the daemon
	// answered with something unexpected, or the output is empty.
	ErrFieldMissing = errors.New("pwrstat output has no Load field")

	// ErrValueUnparseable means the field was present but its value was not a
	// wattage, e.g. "unknown" or a bare number with no unit.
	ErrValueUnparseable = errors.New("pwrstat Load value is not a wattage")
)

// loadPattern matches the leading "66 Watt" of a Load value. The value is
// already trimmed, so ^ anchors at the first byte, and the trailing "(13 %)"
// is never examined.
var loadPattern = regexp.MustCompile(`^([0-9]+(?:\.[0-9]+)?)\s+[Ww]att`)

// ParseLoad extracts the UPS load in watts from pwrstat output, for example
// "Load......................... 66 Watt(13 %)".
//
// A load of zero is a valid reading and is not an error.
//
// It returns ErrFieldMissing when no "Load" field is present, and
// ErrValueUnparseable (wrapping the offending value) when the field exists but
// its value is not a wattage.
func ParseLoad(output string) (float64, error) {
	for _, line := range strings.Split(output, "\n") {
		label, value, ok := splitField(line)
		if !ok || label != "Load" {
			continue
		}

		matches := loadPattern.FindStringSubmatch(value)
		if len(matches) < 2 {
			return 0, fmt.Errorf("%w: %q", ErrValueUnparseable, value)
		}

		load, err := strconv.ParseFloat(matches[1], 64)
		if err != nil {
			return 0, fmt.Errorf("%w: %q: %v", ErrValueUnparseable, value, err)
		}
		return load, nil
	}

	return 0, ErrFieldMissing
}

// splitField splits a dotted-leader line into its label and value.
// Labels never contain dots, so the first run of two or more dots separates
// them; values may contain dots ("45 min.", "2026/11/02 23:17:34").
func splitField(line string) (label, value string, ok bool) {
	start := strings.Index(line, "..")
	if start < 0 {
		return "", "", false
	}
	label = strings.TrimSpace(line[:start])
	value = strings.TrimSpace(strings.TrimLeft(line[start:], "."))
	return label, value, true
}

// IsUnusable reports whether err means the pwrstat binary cannot be run at all,
// as opposed to running and failing.
//
// It matches a missing or non-executable binary (fs.ErrNotExist,
// fs.ErrPermission, exec.ErrNotFound), typically caused by a wrong PWRSTAT_PATH
// or by invoking the vendor CLI without sufficient privileges.
//
// It deliberately does not match a non-zero exit status or a context error: a
// stopped daemon or a slow call is transient, and treating either as fatal would
// turn a temporary outage into a restart loop.
// IsUnusable reports whether err means the CLI itself cannot be executed, as
// opposed to a transient failure such as a stopped daemon, a timeout, or output
// drift. Unusable errors do not heal on their own, so startup treats them as
// fatal.
func IsUnusable(err error) bool {
	return errors.Is(err, fs.ErrPermission) ||
		errors.Is(err, fs.ErrNotExist) ||
		errors.Is(err, exec.ErrNotFound)
}

// Collect runs "pwrstat -status" and returns the current load in watts.
//
// The invocation is bounded by timeout; the child process is killed if it
// overruns, so a wedged daemon cannot stall the caller. LC_ALL and LANG are
// pinned to C so that the output labels stay in English regardless of the host
// locale; ParseLoad matches those labels literally.
//
// Errors fall into three classes, kept distinct so the caller can choose between
// "refuse to start" and "retry on the next tick":
//
//   - IsUnusable(err): the binary cannot be executed at all (wrong path, missing
//     package, insufficient permissions). This does not resolve itself at run
//     time and should be fatal at startup.
//   - A context error: the timeout fired, or the caller's context was cancelled.
//     Transient by definition.
//   - Anything else: a non-zero exit, usually the daemon being down, or a parse
//     error wrapping ErrFieldMissing / ErrValueUnparseable. The daemon's stderr
//     is included in the message when it printed one.
//
// A non-nil error always means there is no usable reading; never publish the
// zero value that accompanies it, since zero is itself a valid load.
// Collect runs the pwrstat CLI at path and returns the UPS load in watts. The
// call is bounded by timeout plus the child's WaitDelay.
//
// Errors keep their sentinels so callers can classify them: exec and permission
// failures still match fs and exec errors (see IsUnusable), a timeout wraps
// context.DeadlineExceeded, and a diagnostic printed by the daemon on stderr is
// included in the message. Parse failures wrap ErrFieldMissing or
// ErrValueUnparseable.
func Collect(ctx context.Context, path string, timeout time.Duration) (float64, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, path, "-status")
	cmd.Env = append(os.Environ(), "LC_ALL=C", "LANG=C")

	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	cmd.WaitDelay = 2 * time.Second
	out, err := cmd.Output()
	if err != nil {
		if IsUnusable(err) {
			return 0, fmt.Errorf("run %s: %w", path, err) // fatal class
		}
		// Inspect the context this call created rather than the error chain: whether
		// a killed child surfaces as a context error or as *exec.ExitError depends
		// on timing, while ctx.Err() is non-nil exactly when our deadline fired or
		// the caller was cancelled.
		if ctxErr := ctx.Err(); ctxErr != nil {
			if errors.Is(ctxErr, context.DeadlineExceeded) {
				return 0, fmt.Errorf("%s -status timed out after %s: %w", path, timeout, ctxErr)
			}
			return 0, fmt.Errorf("%s -status cancelled: %w", path, ctxErr)
		}
		if msg := strings.TrimSpace(stderr.String()); msg != "" {
			return 0, fmt.Errorf("%s -status: %w: %s", path, err, msg) // daemon class
		}
		return 0, fmt.Errorf("%s -status: %w", path, err)
	}

	return ParseLoad(string(out))
}
