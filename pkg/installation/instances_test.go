package installation_test

import (
	"errors"
	"strings"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/installation"
)

func TestInstanceValidate(t *testing.T) {
	t.Parallel()

	long := strings.Repeat("a", installation.MaxInstanceFieldLength+1)
	edge := strings.Repeat("a", installation.MaxInstanceFieldLength)

	tests := []struct {
		name    string
		id      string
		version string
		wantErr error
	}{
		{name: "release", id: "i-01", version: "v0.2.0", wantErr: nil},
		{name: "pre-release with plus", id: "host_1.a", version: "v0.2.0-rc.1+build", wantErr: nil},
		{name: "dev build", id: "x", version: "dev", wantErr: nil},
		{name: "longest allowed", id: edge, version: edge, wantErr: nil},
		{name: "empty id", id: "", version: "v0.2.0", wantErr: installation.ErrInvalidInstance},
		{name: "empty version", id: "i-01", version: "", wantErr: installation.ErrInvalidInstance},
		{name: "id too long", id: long, version: "v0.2.0", wantErr: installation.ErrInvalidInstance},
		{name: "version too long", id: "i-01", version: long, wantErr: installation.ErrInvalidInstance},
		{name: "space in id", id: "i 01", version: "v0.2.0", wantErr: installation.ErrInvalidInstance},
		{name: "newline in id", id: "i\n01", version: "v0.2.0", wantErr: installation.ErrInvalidInstance},
		{name: "slash in id", id: "../x", version: "v0.2.0", wantErr: installation.ErrInvalidInstance},
		{name: "quote in version", id: "i-01", version: "v0.2.0'", wantErr: installation.ErrInvalidInstance},
		{name: "non ASCII letter", id: "iß", version: "v0.2.0", wantErr: installation.ErrInvalidInstance},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			inst := installation.Instance{StartedAt: time.Time{}, ID: tt.id, BinaryVersion: tt.version}

			if err := inst.Validate(); !errors.Is(err, tt.wantErr) {
				t.Fatalf("Validate() = %v, want %v", err, tt.wantErr)
			}
		})
	}
}

func TestValidInstanceID(t *testing.T) {
	t.Parallel()

	if err := installation.ValidInstanceID("i-01"); err != nil {
		t.Fatalf("ValidInstanceID(i-01) = %v, want nil", err)
	}

	for _, bad := range []string{"", "a b", "a/b", "a;b", strings.Repeat("a", installation.MaxInstanceFieldLength+1)} {
		if err := installation.ValidInstanceID(bad); !errors.Is(err, installation.ErrInvalidInstance) {
			t.Errorf("ValidInstanceID(%q) = %v, want ErrInvalidInstance", bad, err)
		}
	}
}
