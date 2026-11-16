package tempadmin_test

import (
	"errors"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/setting"
	"opendustpan.org/opendustpan/pkg/tempadmin"
)

func TestParseTempAdminLimitRange(t *testing.T) {
	t.Parallel()

	tests := []struct {
		value string
		want  time.Duration
	}{
		{"1ms", time.Millisecond},
		{"1h", time.Hour},
		{"3600s", time.Hour},
		{"24h", 24 * time.Hour},
	}

	for _, tc := range tests {
		got, err := tempadmin.ParseTempAdminLimit(tc.value)
		if err != nil || got != tc.want {
			t.Fatalf("ParseTempAdminLimit(%q) = %s, %v, want %s", tc.value, got, err, tc.want)
		}
	}
}

func TestParseTempAdminLimitHostile(t *testing.T) {
	t.Parallel()

	for _, value := range []string{
		"48h", "24h1ms", "25h", "-1h", "-1ms", "0", "0s", "500us", "1h0m0.0001s", "soon", "", "3600",
		"1h; DROP TABLE settings", "NaN", "1e3h",
	} {
		if got, err := tempadmin.ParseTempAdminLimit(value); !errors.Is(err, tempadmin.ErrSessionLimit) {
			t.Fatalf("ParseTempAdminLimit(%q) = %s, %v, want ErrSessionLimit", value, got, err)
		}
	}
}

// TestSessionLimitReducesSecurity checks the rule of the fresh step: a longer
// limit is weaker, and a value that does not parse counts as weaker too.
func TestSessionLimitReducesSecurity(t *testing.T) {
	t.Parallel()

	var declared *setting.Declaration

	for _, d := range tempadmin.SessionLimitSettingDeclarations() {
		if d.Key == tempadmin.SettingSessionLimit {
			declared = &d
		}
	}

	if declared == nil || declared.ReducesSecurity == nil {
		t.Fatal("tempadmin.session-limit declares no ReducesSecurity rule")
	}

	tests := []struct {
		name      string
		old, next string
		want      bool
	}{
		{"a longer limit", "1h", "24h", true},
		{"a limit one millisecond longer", "1h", "1h0.001s", true},
		{"the same limit", "1h", "1h", false},
		{"a shorter limit", "1h", "30m", false},
		{"a stored value that does not parse", "garbage", "30m", true},
		{"a stored value above the range", "48h", "30m", true},
		{"a new value that does not parse", "1h", "garbage", true},
		{"both values do not parse", "", "", true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if got := declared.ReducesSecurity(tc.old, tc.next); got != tc.want {
				t.Fatalf("ReducesSecurity(%q, %q) = %v, want %v", tc.old, tc.next, got, tc.want)
			}
		})
	}
}
