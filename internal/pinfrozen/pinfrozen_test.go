package pinfrozen_test

import (
	"errors"
	"fmt"
	"io/fs"
	"testing"

	"opendustpan.org/opendustpan/internal/pinfrozen"
)

const (
	descriptionV2       = `{"info":{"version":"0.2.0"},"paths":{}}`
	descriptionV3       = `{"info":{"version":"0.3.0"},"paths":{}}`
	descriptionV2Longer = `{"info":{"version":"0.2.0"},"paths":{"/x":{}}}`
	schemaVersion       = `{"info":{"version":"0.2.0"},"components":{"schemas":{"s":{"version":2}}}}`
	schemaVersionMoved  = `{"info":{"version":"0.2.0"},"components":{"schemas":{"s":{"version":3}}}}`
)

// errStoreDown is a read error that is not a missing file.
var errStoreDown = errors.New("the object store is down")

// files returns a Reader over a map from path to content. A missing path wraps
// fs.ErrNotExist, as a reader of a Git revision does.
func files(m map[string]string) pinfrozen.Reader {
	return func(path string) ([]byte, error) {
		content, ok := m[path]
		if !ok {
			return nil, fmt.Errorf("%s: %w", path, fs.ErrNotExist)
		}

		return []byte(content), nil
	}
}

// broken returns a Reader that fails with a read error that is not a missing file.
func broken(string) ([]byte, error) {
	return nil, errStoreDown
}

// TestCheck sends each kind of branch to the rule: a change of the pin, a
// change of info.version, a change of the description that keeps its version,
// and a branch that changes neither.
func TestCheck(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		changed []string
		base    map[string]string
		head    map[string]string
		want    error
		wantErr bool
	}{
		{
			name:    "neither the pin nor the version changed",
			changed: []string{"internal/api/apidesc/document.go"},
			base:    map[string]string{pinfrozen.DescriptionPath: descriptionV2},
			head:    map[string]string{pinfrozen.DescriptionPath: descriptionV2},
			want:    nil,
			wantErr: false,
		},
		{
			name:    "the pin changed",
			changed: []string{pinfrozen.PinPath},
			base:    map[string]string{},
			head:    map[string]string{},
			want:    pinfrozen.ErrPinChanged,
			wantErr: true,
		},
		{
			name:    "info.version changed",
			changed: []string{pinfrozen.DescriptionPath},
			base:    map[string]string{pinfrozen.DescriptionPath: descriptionV2},
			head:    map[string]string{pinfrozen.DescriptionPath: descriptionV3},
			want:    pinfrozen.ErrVersionChanged,
			wantErr: true,
		},
		{
			name:    "the description changed and its version did not",
			changed: []string{pinfrozen.DescriptionPath},
			base:    map[string]string{pinfrozen.DescriptionPath: descriptionV2},
			head:    map[string]string{pinfrozen.DescriptionPath: descriptionV2Longer},
			want:    nil,
			wantErr: false,
		},
		{
			name:    "a version under a schema is not info.version",
			changed: []string{pinfrozen.DescriptionPath},
			base:    map[string]string{pinfrozen.DescriptionPath: schemaVersion},
			head:    map[string]string{pinfrozen.DescriptionPath: schemaVersionMoved},
			want:    nil,
			wantErr: false,
		},
		{
			name:    "the description was added with a version",
			changed: []string{pinfrozen.DescriptionPath},
			base:    map[string]string{},
			head:    map[string]string{pinfrozen.DescriptionPath: descriptionV2},
			want:    pinfrozen.ErrVersionChanged,
			wantErr: true,
		},
		{
			name:    "the description was removed",
			changed: []string{pinfrozen.DescriptionPath},
			base:    map[string]string{pinfrozen.DescriptionPath: descriptionV2},
			head:    map[string]string{},
			want:    pinfrozen.ErrVersionChanged,
			wantErr: true,
		},
		{
			name:    "the pin and the version both changed",
			changed: []string{pinfrozen.PinPath, pinfrozen.DescriptionPath},
			base:    map[string]string{pinfrozen.DescriptionPath: descriptionV2},
			head:    map[string]string{pinfrozen.DescriptionPath: descriptionV3},
			want:    pinfrozen.ErrPinChanged,
			wantErr: true,
		},
		{
			name:    "the head description is not JSON",
			changed: []string{pinfrozen.DescriptionPath},
			base:    map[string]string{pinfrozen.DescriptionPath: descriptionV2},
			head:    map[string]string{pinfrozen.DescriptionPath: "{"},
			want:    nil,
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			assertCheck(t, pinfrozen.Check(tt.changed, files(tt.base), files(tt.head)), tt.want, tt.wantErr)
		})
	}
}

// assertCheck compares the result of Check with the sentinel that a case names.
// A case with no sentinel and wantErr set needs any error.
func assertCheck(t *testing.T, err, want error, wantErr bool) {
	t.Helper()

	switch {
	case want != nil:
		if !errors.Is(err, want) {
			t.Errorf("Check() = %v, want %v", err, want)
		}
	case wantErr:
		if err == nil {
			t.Error("Check() = nil, want an error")
		}
	default:
		if err != nil {
			t.Errorf("Check() = %v, want nil", err)
		}
	}
}

// TestCheckFailsClosedOnUnreadableDescription sends a read error that is not a
// missing file directly to the rule, on each side of the comparison.
func TestCheckFailsClosedOnUnreadableDescription(t *testing.T) {
	t.Parallel()

	changed := []string{pinfrozen.DescriptionPath}

	if err := pinfrozen.Check(changed, broken, files(nil)); !errors.Is(err, errStoreDown) {
		t.Errorf("Check() with an unreadable base = %v, want the read error", err)
	}

	if err := pinfrozen.Check(changed, files(nil), broken); !errors.Is(err, errStoreDown) {
		t.Errorf("Check() with an unreadable head = %v, want the read error", err)
	}
}
