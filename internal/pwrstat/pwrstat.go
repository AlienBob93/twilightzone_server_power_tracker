// Package pwrstat reads the UPS load from CyberPower's pwrstat status output.
package pwrstat

import (
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
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
