package installation_test

import (
	"errors"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/pkg/installation"
)

func TestParseKind(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		raw     string
		want    installation.Kind
		wantErr error
	}{
		{name: "dev", raw: "dev", want: installation.KindDev, wantErr: nil},
		{name: "production", raw: "production", want: installation.KindProduction, wantErr: nil},
		{name: "empty", raw: "", want: "", wantErr: installation.ErrInvalidKind},
		{name: "other word", raw: "staging", want: "", wantErr: installation.ErrInvalidKind},
		{name: "wrong case", raw: "Dev", want: "", wantErr: installation.ErrInvalidKind},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := installation.ParseKind(tt.raw)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("ParseKind(%q) error = %v, want %v", tt.raw, err, tt.wantErr)
			}

			if got != tt.want {
				t.Fatalf("ParseKind(%q) = %q, want %q", tt.raw, got, tt.want)
			}
		})
	}
}

func TestMismatchError(t *testing.T) {
	t.Parallel()

	err := error(&installation.MismatchError{Stored: installation.KindDev, Want: installation.KindProduction})

	if !errors.Is(err, installation.ErrKindMismatch) {
		t.Fatalf("errors.Is(%v, ErrKindMismatch) = false, want true", err)
	}

	if errors.Is(err, installation.ErrUnavailable) {
		t.Fatalf("errors.Is(%v, ErrUnavailable) = true, want false", err)
	}

	for _, want := range []string{"dev", "production"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("error %q does not name %q", err, want)
		}
	}
}

func TestFromStored(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		raw     string
		want    installation.Kind
		wantErr error
	}{
		{name: "dev", raw: "dev", want: installation.KindDev, wantErr: nil},
		{name: "production", raw: "production", want: installation.KindProduction, wantErr: nil},
		{name: "empty", raw: "", want: "", wantErr: installation.ErrUnavailable},
		{name: "unknown", raw: "staging", want: "", wantErr: installation.ErrUnavailable},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := installation.FromStored(tt.raw)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("FromStored(%q) error = %v, want %v", tt.raw, err, tt.wantErr)
			}

			if got != tt.want {
				t.Fatalf("FromStored(%q) = %q, want %q", tt.raw, got, tt.want)
			}

			if err != nil && tt.raw != "" && strings.Contains(err.Error(), tt.raw) {
				t.Fatalf("error %q holds the stored value %q", err, tt.raw)
			}
		})
	}
}

func TestCheck(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		stored  string
		want    installation.Kind
		wantErr error
	}{
		{name: "not claimed yet", stored: "", want: installation.KindDev, wantErr: nil},
		{name: "same dev", stored: "dev", want: installation.KindDev, wantErr: nil},
		{name: "same production", stored: "production", want: installation.KindProduction, wantErr: nil},
		{
			name:    "dev database, production caller",
			stored:  "dev",
			want:    installation.KindProduction,
			wantErr: installation.ErrKindMismatch,
		},
		{
			name:    "production database, dev caller",
			stored:  "production",
			want:    installation.KindDev,
			wantErr: installation.ErrKindMismatch,
		},
		{
			name:    "unknown stored value",
			stored:  "staging",
			want:    installation.KindDev,
			wantErr: installation.ErrUnavailable,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			err := installation.Check(tt.stored, tt.want)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("Check(%q, %q) error = %v, want %v", tt.stored, tt.want, err, tt.wantErr)
			}
		})
	}
}
