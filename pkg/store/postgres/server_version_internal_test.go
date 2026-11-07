package postgres

import (
	"errors"
	"testing"
)

func TestCheckServerVersion(t *testing.T) {
	t.Parallel()

	cases := []struct {
		input   string
		wantErr bool
	}{
		{input: "100000", wantErr: true},
		{input: "160004", wantErr: true},
		{input: "170000", wantErr: true},
		{input: "180000", wantErr: false},
		{input: "180006", wantErr: false},
		{input: "190001", wantErr: false},
		{input: " 180000 ", wantErr: false},
		{input: "", wantErr: true},
		{input: "0", wantErr: true},
		{input: "-180000", wantErr: true},
		{input: "eighteen", wantErr: true},
	}

	for _, tc := range cases {
		t.Run(tc.input, func(t *testing.T) {
			t.Parallel()

			err := checkServerVersion(tc.input)
			if tc.wantErr {
				if !errors.Is(err, ErrServerVersion) {
					t.Fatalf("checkServerVersion(%q) = %v, want ErrServerVersion", tc.input, err)
				}

				return
			}

			if err != nil {
				t.Fatalf("checkServerVersion(%q) = %v, want nil", tc.input, err)
			}
		})
	}
}
