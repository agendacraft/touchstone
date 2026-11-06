package config

import (
	"math"
	"testing"
)

func TestOutputThresholds(t *testing.T) {
	for _, tc := range []struct {
		name, input string
		want        Thresholds
		bad         bool
	}{
		{"defaults", "", Thresholds{MinSeverity: "info"}, false},
		{"override", "[output]\nmin_severity='warning'\nmin_confidence=0.85", Thresholds{MinSeverity: "warning", MinConfidence: 0.85}, false},
		{"zero", "[output]\nmin_confidence=0", Thresholds{MinSeverity: "info"}, false},
		{"one", "[output]\nmin_confidence=1", Thresholds{MinSeverity: "info", MinConfidence: 1}, false},
		{"bad severity", "[output]\nmin_severity='critical'", Thresholds{}, true},
		{"empty severity", "[output]\nmin_severity=''", Thresholds{}, true},
		{"negative", "[output]\nmin_confidence=-0.1", Thresholds{}, true},
		{"over one", "[output]\nmin_confidence=1.1", Thresholds{}, true},
		{"nan", "[output]\nmin_confidence=nan", Thresholds{}, true},
		{"inf", "[output]\nmin_confidence=inf", Thresholds{}, true},
		{"unknown output", "[output]\nformat='json'", Thresholds{}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg, err := Parse([]byte(tc.input))
			if (err != nil) != tc.bad {
				t.Fatalf("unexpected error: %v", err)
			}
			if !tc.bad && cfg.Output() != tc.want {
				t.Fatalf("got %+v, want %+v", cfg.Output(), tc.want)
			}
		})
	}
	for _, value := range []float64{math.NaN(), math.Inf(1), math.Inf(-1)} {
		if (Thresholds{MinSeverity: "info", MinConfidence: value}).Validate() == nil {
			t.Fatal("non-finite confidence accepted")
		}
	}
}
