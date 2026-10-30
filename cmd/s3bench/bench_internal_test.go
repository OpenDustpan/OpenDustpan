package main

// The tests stay white-box (package main): a main package cannot be
// imported.

import (
	"errors"
	"testing"
)

func TestLinkSplit(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		in        link
		wantUp    latencyAttrs
		wantDown  latencyAttrs
		wantTotal int
	}{
		{
			"zero",
			link{RTTMs: 0, JitterMs: 0},
			latencyAttrs{Latency: 0, Jitter: 0},
			latencyAttrs{Latency: 0, Jitter: 0},
			0,
		},
		{
			"one",
			link{RTTMs: 1, JitterMs: 0},
			latencyAttrs{Latency: 0, Jitter: 0},
			latencyAttrs{Latency: 1, Jitter: 0},
			1,
		},
		{
			"odd",
			link{RTTMs: 65, JitterMs: 4},
			latencyAttrs{Latency: 32, Jitter: 4},
			latencyAttrs{Latency: 33, Jitter: 4},
			65,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			pair := tt.in.split()
			up, down := pair.up, pair.down

			if up != tt.wantUp || down != tt.wantDown || up.Latency+down.Latency != tt.wantTotal {
				t.Fatalf("split(%+v) = %+v, %+v, want %+v, %+v", tt.in, up, down, tt.wantUp, tt.wantDown)
			}
		})
	}
}

func TestQuantile(t *testing.T) {
	t.Parallel()

	samples := []float64{5, 1, 4, 2, 3}

	if got := quantile(samples, 0.5); got != 3 {
		t.Fatalf("median = %v, want 3", got)
	}

	if got := quantile(samples, 0.99); got != 5 {
		t.Fatalf("p99 = %v, want 5", got)
	}

	if got := quantile(nil, 0.5); got != 0 {
		t.Fatalf("median of nothing = %v, want 0", got)
	}

	if samples[0] != 5 {
		t.Fatalf("quantile sorted its input: %v", samples)
	}
}

func TestParseSteps(t *testing.T) {
	t.Parallel()

	got, err := parseSteps("0, 50,1000")
	if err != nil || len(got) != 3 || got[2] != 1000 {
		t.Fatalf("parseSteps = %v, %v, want [0 50 1000]", got, err)
	}

	for _, bad := range []string{"", "-1", "ten", "1,,2"} {
		if _, err := parseSteps(bad); !errors.Is(err, errBadFlag) {
			t.Fatalf("parseSteps(%q) error = %v, want errBadFlag", bad, err)
		}
	}
}

func TestSelectProfiles(t *testing.T) {
	t.Parallel()

	all := standardProfiles()

	got, err := selectProfiles(all, "eu-metro,same-az")
	if err != nil || len(got) != 2 {
		t.Fatalf("selectProfiles = %v, %v, want two profiles", got, err)
	}

	if _, err := selectProfiles(all, "moon"); !errors.Is(err, errBadFlag) {
		t.Fatalf("selectProfiles(moon) error = %v, want errBadFlag", err)
	}
}

func TestFirstField(t *testing.T) {
	t.Parallel()

	text := "processor\t: 0\nmodel name\t: Test CPU @ 1.00GHz\nmodel name\t: other\n"

	if got := firstField(text, "model name"); got != "Test CPU @ 1.00GHz" {
		t.Fatalf("firstField = %q", got)
	}

	if got := firstField(text, "MemTotal"); got != "" {
		t.Fatalf("firstField of a missing name = %q, want empty", got)
	}
}

func TestRedact(t *testing.T) {
	t.Parallel()

	creds := secrets{byName: map[string]string{"pg-password": "s3cr3t-value"}}

	if got := creds.redact("connect: password s3cr3t-value refused"); got != "connect: password [redacted] refused" {
		t.Fatalf("redact = %q", got)
	}
}
