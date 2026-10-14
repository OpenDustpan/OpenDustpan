package main

import (
	"errors"
	"testing"
	"time"
)

// TestParseRetention proves the soft-delete retention check: a Go duration of
// at least one hour passes with no maximum, and anything else fails with
// errRetention.
func TestParseRetention(t *testing.T) {
	t.Parallel()

	cases := []struct {
		in      string
		want    time.Duration
		wantErr bool
	}{
		{in: "720h", want: 720 * time.Hour, wantErr: false},
		{in: "1h", want: time.Hour, wantErr: false},
		{in: "87600h", want: 87600 * time.Hour, wantErr: false},
		{in: "59m", want: 0, wantErr: true},
		{in: "0", want: 0, wantErr: true},
		{in: "-24h", want: 0, wantErr: true},
		{in: "30d", want: 0, wantErr: true},
		{in: "", want: 0, wantErr: true},
	}

	for _, tc := range cases {
		got, err := parseRetention(tc.in)
		if tc.wantErr {
			if !errors.Is(err, errRetention) {
				t.Errorf("parseRetention(%q): err = %v, want errRetention", tc.in, err)
			}

			continue
		}

		if err != nil || got != tc.want {
			t.Errorf("parseRetention(%q) = %v, %v, want %v", tc.in, got, err, tc.want)
		}
	}
}
