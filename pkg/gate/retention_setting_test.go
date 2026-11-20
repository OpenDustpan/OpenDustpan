package gate_test

import (
	"errors"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/gate"
)

// TestRetentionSettingCheck checks the values that the retention setting
// accepts and the hostile values that it refuses.
func TestRetentionSettingCheck(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		value   string
		wantErr bool
	}{
		{"default keeps forever", "0", false},
		{"minimum", "24", false},
		{"one year", "8760", false},
		{"maximum", "87600", false},
		{"below minimum", "23", true},
		{"one hour", "1", true},
		{"above maximum", "87601", true},
		{"very long number", "99999999999999999999999", true},
		{"negative", "-1", true},
		{"plus sign", "+24", true},
		{"leading zero", "024", true},
		{"double zero", "00", true},
		{"exponent", "1e3", true},
		{"leading space", " 24", true},
		{"trailing space", "24 ", true},
		{"trailing newline", "24\n", true},
		{"fraction", "24.0", true},
		{"text", "abc", true},
		{"empty", "", true},
		{"arabic-indic digits", "٢٤", true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			err := gate.RetentionCheck(tc.value)
			if (err != nil) != tc.wantErr {
				t.Fatalf("RetentionCheck(%q) = %v, want error %v", tc.value, err, tc.wantErr)
			}

			if err != nil && !errors.Is(err, gate.ErrInvalidRetention) {
				t.Errorf("RetentionCheck(%q) = %v, want ErrInvalidRetention", tc.value, err)
			}
		})
	}
}

// TestRetentionSettingReducesSecurity checks which changes end records sooner.
func TestRetentionSettingReducesSecurity(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		old, next string
		want      bool
	}{
		{"forever to period", "0", "720", true},
		{"shorter", "8760", "720", true},
		{"old does not parse", "abc", "720", true},
		{"new does not parse", "720", "abc", true},
		{"both empty", "", "", true},
		{"longer", "720", "8760", false},
		{"period to forever", "720", "0", false},
		{"same period", "720", "720", false},
		{"same forever", "0", "0", false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if got := gate.RetentionReducesSecurity(tc.old, tc.next); got != tc.want {
				t.Errorf("RetentionReducesSecurity(%q, %q) = %v, want %v", tc.old, tc.next, got, tc.want)
			}
		})
	}
}

// TestRetentionPeriod checks the one parse of the stored value.
func TestRetentionPeriod(t *testing.T) {
	t.Parallel()

	tests := []struct {
		value   string
		period  time.Duration
		forever bool
	}{
		{"0", 0, true},
		{"24", 24 * time.Hour, false},
		{"87600", 87600 * time.Hour, false},
	}

	for _, tc := range tests {
		period, forever, err := gate.RetentionPeriod(tc.value)
		if err != nil || forever != tc.forever || period != tc.period {
			t.Errorf("RetentionPeriod(%q) = %v, %v, %v, want %v, %v, nil", tc.value, period, forever, err,
				tc.period, tc.forever)
		}
	}
}

// TestRetentionPeriodRefusesHostileText checks that a refused text gives no
// period and no keep-forever, so a caller cannot read it as either.
func TestRetentionPeriodRefusesHostileText(t *testing.T) {
	t.Parallel()

	for _, bad := range []string{"", "23", "87601", "-1", "024", "1e3", " 24", "abc"} {
		period, forever, err := gate.RetentionPeriod(bad)
		if !errors.Is(err, gate.ErrInvalidRetention) || forever || period != 0 {
			t.Errorf("RetentionPeriod(%q) = %v, %v, %v, want 0, false, ErrInvalidRetention", bad, period, forever, err)
		}
	}
}
