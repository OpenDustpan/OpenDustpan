package main

import (
	"errors"
	"fmt"
	"testing"

	"opendustpan.org/opendustpan/pkg/installation"
)

func TestKindRefusal(t *testing.T) {
	t.Parallel()

	other := errDiskFull

	tests := []struct {
		name    string
		err     error
		command string
		want    string
		wantIs  error
	}{
		{
			name: "serve on a database of dev",
			err: fmt.Errorf(
				"open sqlite store: %w",
				&installation.MismatchError{Stored: installation.KindDev, Want: installation.KindProduction},
			),
			command: commandServe,
			want:    "opendustpan dev made this database: serve refuses it. Set --database to a new database.",
			wantIs:  installation.ErrKindMismatch,
		},
		{
			name:    "dev on a database of serve",
			err:     &installation.MismatchError{Stored: installation.KindProduction, Want: installation.KindDev},
			command: commandDev,
			want:    "opendustpan serve made this database: dev refuses it. Set a new --data-dir.",
			wantIs:  installation.ErrKindMismatch,
		},
		{name: "another error stays as it is", err: other, command: commandServe, want: "disk full", wantIs: other},
		{name: "no error stays no error", err: nil, command: commandDev, want: "", wantIs: nil},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := kindRefusal(tt.err, tt.command)

			if tt.err == nil {
				if got != nil {
					t.Fatalf("kindRefusal(nil) = %v", got)
				}

				return
			}

			if got.Error() != tt.want {
				t.Fatalf("text = %q, want %q", got.Error(), tt.want)
			}

			if !errors.Is(got, tt.wantIs) {
				t.Fatalf("errors.Is(%v, %v) = false", got, tt.wantIs)
			}
		})
	}
}

func TestKindChange(t *testing.T) {
	t.Parallel()

	for _, kind := range []installation.Kind{installation.KindDev, installation.KindProduction} {
		change := kindChange(kind)
		if change.Key != "installation-kind" || change.New != kind.String() || change.Old != nil {
			t.Errorf("kindChange(%s) = %+v", kind, change)
		}
	}
}
