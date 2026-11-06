package dustpan_test

import (
	"errors"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/dustpan"
	"opendustpan.org/opendustpan/pkg/history"
)

// TestNewServerChecksJobRetention proves that WithRetention accepts the range
// from history.MinRetention to history.MaxRetention and that NewServer
// refuses any other period for plans or job logs.
func TestNewServerChecksJobRetention(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name      string
		retention history.RetentionSettings
		wantErr   bool
	}{
		{name: "defaults", retention: history.DefaultRetentionSettings(), wantErr: false},
		{
			name:      "two days of logs",
			retention: history.RetentionSettings{Plan: history.DefaultRetention, JobLog: 48 * time.Hour},
			wantErr:   false,
		},
		{
			name: "minimum",
			retention: history.RetentionSettings{
				Plan:   history.MinRetention,
				JobLog: history.MinRetention,
			},
			wantErr: false,
		},
		{
			name: "maximum",
			retention: history.RetentionSettings{
				Plan:   history.MaxRetention,
				JobLog: history.MaxRetention,
			},
			wantErr: false,
		},
		{name: "zero value", retention: history.RetentionSettings{Plan: 0, JobLog: 0}, wantErr: true},
		{
			name:      "plan below minimum",
			retention: history.RetentionSettings{Plan: time.Hour, JobLog: history.DefaultRetention}, wantErr: true,
		},
		{
			name:      "negative job log",
			retention: history.RetentionSettings{Plan: history.DefaultRetention, JobLog: -time.Hour}, wantErr: true,
		},
		{
			name:      "job log above maximum",
			retention: history.RetentionSettings{Plan: history.DefaultRetention, JobLog: history.MaxRetention + 1},
			wantErr:   true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			st, blobs, authn := requireServerPorts(t)
			opts := append(fullServerOptions(t, st, blobs, authn), dustpan.WithRetention(tc.retention))

			_, err := dustpan.NewServer(opts...)
			if !tc.wantErr {
				if err != nil {
					t.Fatalf("NewServer: %v", err)
				}

				return
			}

			if !errors.Is(err, dustpan.ErrInvalidOption) || !errors.Is(err, history.ErrInvalidRetention) {
				t.Fatalf("NewServer err = %v, want ErrInvalidOption and ErrInvalidRetention", err)
			}
		})
	}
}
