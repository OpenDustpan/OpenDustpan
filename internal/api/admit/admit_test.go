package admit_test

import (
	"errors"
	"testing"

	"opendustpan.org/opendustpan/internal/api/admit"
	"opendustpan.org/opendustpan/internal/api/admit/admittest"
)

var errStore = errors.New("store down")

func TestStart(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		src     admit.Source
		wantErr error
	}{
		{"no source admits", nil, nil},
		{"a worker admits", admittest.Worker(), nil},
		{"no worker refuses", admittest.NoWorker(), admit.ErrNoWorker},
		{"another error is not hidden", &admittest.Source{Err: errStore}, errStore},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			err := admit.Start(t.Context(), tc.src)
			if !errors.Is(err, tc.wantErr) || (tc.wantErr == nil && err != nil) {
				t.Fatalf("Start = %v, want %v", err, tc.wantErr)
			}
		})
	}
}
