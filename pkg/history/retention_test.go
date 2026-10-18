package history_test

import (
	"errors"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/history"
)

func TestRetentionSettings(t *testing.T) {
	t.Parallel()

	cases := []struct {
		want     error
		name     string
		settings history.RetentionSettings
	}{
		{name: "the defaults", settings: history.DefaultRetentionSettings(), want: nil},
		{
			name:     "the minimum",
			settings: history.RetentionSettings{Plan: history.MinRetention, JobLog: history.MinRetention},
			want:     nil,
		},
		{
			name:     "a plan period below the minimum",
			settings: history.RetentionSettings{Plan: time.Hour, JobLog: history.DefaultRetention},
			want:     history.ErrInvalidRetention,
		},
		{
			name:     "a job log period below the minimum",
			settings: history.RetentionSettings{Plan: history.DefaultRetention, JobLog: history.MinRetention - 1},
			want:     history.ErrInvalidRetention,
		},
		{
			name:     "a zero period",
			settings: history.RetentionSettings{Plan: 0, JobLog: 0},
			want:     history.ErrInvalidRetention,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if err := tc.settings.Validate(); !errors.Is(err, tc.want) {
				t.Errorf("Validate err = %v, want %v", err, tc.want)
			}
		})
	}
}

func TestRetentionDefaults(t *testing.T) {
	t.Parallel()

	if history.DefaultRetention != 400*24*time.Hour {
		t.Errorf("DefaultRetention = %s, want 400 days", history.DefaultRetention)
	}

	if history.MinRetention != 24*time.Hour {
		t.Errorf("MinRetention = %s, want 24h", history.MinRetention)
	}

	names := []string{
		history.PlanRetentionFlag, history.PlanRetentionEnv,
		history.JobLogRetentionFlag, history.JobLogRetentionEnv,
	}
	want := []string{"plan-retention", "DUSTPAN_PLAN_RETENTION", "job-log-retention", "DUSTPAN_JOB_LOG_RETENTION"}

	for i := range names {
		if names[i] != want[i] {
			t.Errorf("setting name %d = %q, want %q", i, names[i], want[i])
		}
	}

	created := time.Date(2026, time.October, 17, 9, 0, 0, 0, time.UTC)
	if got := history.ExpiresAt(created, history.DefaultRetention); !got.Equal(created.AddDate(0, 0, 400)) {
		t.Errorf("ExpiresAt = %s, want 400 days after the creation", got)
	}
}
