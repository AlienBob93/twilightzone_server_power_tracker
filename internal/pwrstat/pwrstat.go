package pwrstat

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// loadPattern matches the leading "66 Watt" of a Load value. The value is
// already trimmed, so ^ anchors at the first byte, and the trailing "(13 %)"
// is never examined.
var loadPattern = regexp.MustCompile(`^([0-9]+(?:\.[0-9]+)?)\s+[Ww]att`)

// ParseLoad extracts the UPS load in watts from pwrstat output.
//
// It searches for a line with the "Load" field and parses the numeric wattage
// value, for example "Load......................... 66 Watt(13 %)".
//
// Parameters:
//   - output: the full pwrstat output string.
//
// Returns:
//   - float64: the parsed load in watts.
//   - error: an error if the load field or a valid wattage value is not found.
func ParseLoad(output string) (float64, error) {
	for _, line := range strings.Split(output, "\n") {
		label, value, ok := splitField(line)
		if !ok || label != "Load" {
			continue
		}

		matches := loadPattern.FindStringSubmatch(value)
		if len(matches) < 2 {
			return 0, fmt.Errorf("load value not found")
		}
		load, err := strconv.ParseFloat(matches[1], 64)
		if err != nil {
			return 0, fmt.Errorf("load value %q: %w", value, err)
		}
		return load, nil
	}

	return 0, fmt.Errorf("load field not found")
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
