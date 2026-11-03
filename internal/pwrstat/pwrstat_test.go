package pwrstat

import (
	"errors"
	"strings"
	"testing"
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
