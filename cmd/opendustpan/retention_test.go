package main

import (
	"errors"
	"strings"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/errcode"
	"opendustpan.org/opendustpan/pkg/history"
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

// TestCheckJobRetention proves the check of the plan and job log flags: a Go
// duration from 24h to 87600h passes, and anything else fails with the
// catalog code, errJobRetention and the name of the flag.
func TestCheckJobRetention(t *testing.T) {
	t.Parallel()

	const day = 24 * time.Hour

	maxed := history.RetentionSettings{Plan: history.MaxRetention, JobLog: history.MaxRetention}
	none := history.RetentionSettings{Plan: 0, JobLog: 0}

	cases := []struct {
		name    string
		plan    string
		jobLog  string
		want    history.RetentionSettings
		wantErr string
	}{
		{"defaults", "9600h", "9600h", history.DefaultRetentionSettings(), ""},
		{"two days", "48h", "48h", history.RetentionSettings{Plan: 2 * day, JobLog: 2 * day}, ""},
		{"minimum", "24h", "24h", history.RetentionSettings{Plan: day, JobLog: day}, ""},
		{"maximum", "87600h", "87600h", maxed, ""},
		{"plan below minimum", "23h", "48h", none, "plan-retention"},
		{"job log zero", "48h", "0", none, "job-log-retention"},
		{"job log negative", "48h", "-48h", none, "job-log-retention"},
		{"plan above maximum", "87601h", "48h", none, "plan-retention"},
		{"job log far above maximum", "48h", "2562047h", none, "job-log-retention"},
		{"plan not a duration", "400d", "48h", none, "plan-retention"},
		{"job log empty", "48h", "", none, "job-log-retention"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			var f serveFlags

			f.planRetention, f.jobLogRetention = tc.plan, tc.jobLog
			err := f.checkJobRetention()

			if tc.wantErr == "" {
				if err != nil || f.jobRetention != tc.want {
					t.Fatalf("checkJobRetention = %v, %+v, want %+v", err, f.jobRetention, tc.want)
				}

				return
			}

			if err == nil || !errors.Is(err, errJobRetention) ||
				!strings.Contains(err.Error(), errcode.SettingsRetentionInvalid) ||
				!strings.Contains(err.Error(), "--"+tc.wantErr) {
				t.Fatalf("checkJobRetention err = %v, want the code and --%s", err, tc.wantErr)
			}
		})
	}
}
