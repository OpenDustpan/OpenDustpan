package tempadmin_test

import (
	"errors"
	"testing"
	"time"

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
