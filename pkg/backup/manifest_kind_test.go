package backup_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/pkg/backup"
	"opendustpan.org/opendustpan/pkg/installation"
)

// kindReaderFunc is a backup.KindReader from a function.
type kindReaderFunc func(ctx context.Context) (installation.Kind, error)

// ReadKind implements backup.KindReader.
func (f kindReaderFunc) ReadKind(ctx context.Context) (installation.Kind, error) {
	return f(ctx)
}

// writeKindManifest runs a backup and rewrites its manifest with change, then
// returns the fixture. The manifest is not authenticated, so a person who can
// write to the target can edit it like this.
func writeKindManifest(t *testing.T, change func(manifest string) string) (*fixture, backup.ID) {
	t.Helper()

	sql := []byte("sqlite-image-bytes")
	fix := newFixture(t, &stubExporter{sum: testSummary(), data: sql, err: nil}, 0)

	const id = backup.ID("bkp-kind")

	mustRun(t, fix.service, id)

	key := "backups/" + id.String() + "/manifest.json"
	edited := change(string(fix.target.readObject(t, key)))

	if err := fix.target.Delete(t.Context(), mustKey(t, key)); err != nil {
		t.Fatalf("delete manifest: %v", err)
	}

	fix.target.storeObject(t, key, []byte(edited))

	return fix, id
}

// TestManifestKindIsRequired sends manifests of format 3 with a missing,
// empty, upper case, unknown or wrongly typed kind to the parse alone (layer
// 1a). Each is refused with ErrManifestKindInvalid. A manifest of format 2 is
// refused by the format check, whatever its kind.
func TestManifestKindIsRequired(t *testing.T) {
	t.Parallel()

	const kindKey = `"installationKind":"production"`

	tests := []struct {
		name    string
		change  func(string) string
		wantErr error
	}{
		{name: "production", change: func(m string) string { return m }, wantErr: nil},
		{
			name:   "dev",
			change: func(m string) string { return strings.Replace(m, kindKey, `"installationKind":"dev"`, 1) },
			wantErr: nil,
		},
		{
			name:    "missing",
			change:  func(m string) string { return strings.Replace(m, `,`+kindKey, ``, 1) },
			wantErr: backup.ErrManifestKindInvalid,
		},
		{
			name:    "empty",
			change:  func(m string) string { return strings.Replace(m, kindKey, `"installationKind":""`, 1) },
			wantErr: backup.ErrManifestKindInvalid,
		},
		{
			name:    "null",
			change:  func(m string) string { return strings.Replace(m, kindKey, `"installationKind":null`, 1) },
			wantErr: backup.ErrManifestKindInvalid,
		},
		{
			name:    "upper case",
			change:  func(m string) string { return strings.Replace(m, kindKey, `"installationKind":"DEV"`, 1) },
			wantErr: backup.ErrManifestKindInvalid,
		},
		{
			name:    "unknown",
			change:  func(m string) string { return strings.Replace(m, kindKey, `"installationKind":"staging"`, 1) },
			wantErr: backup.ErrManifestKindInvalid,
		},
		{
			name:    "number",
			change:  func(m string) string { return strings.Replace(m, kindKey, `"installationKind":1`, 1) },
			wantErr: backup.ErrManifestKindInvalid,
		},
		{
			name:    "array",
			change:  func(m string) string { return strings.Replace(m, kindKey, `"installationKind":["dev"]`, 1) },
			wantErr: backup.ErrManifestKindInvalid,
		},
		{
			name:    "object",
			change:  func(m string) string { return strings.Replace(m, kindKey, `"installationKind":{}`, 1) },
			wantErr: backup.ErrManifestKindInvalid,
		},
		{
			name:    "boolean",
			change:  func(m string) string { return strings.Replace(m, kindKey, `"installationKind":true`, 1) },
			wantErr: backup.ErrManifestKindInvalid,
		},
		{
			name:    "format 2 with a valid kind",
			change:  func(m string) string { return strings.Replace(m, `"format":3`, `"format":2`, 1) },
			wantErr: backup.ErrCorrupt,
		},
		{
			name: "format 2 with no kind",
			change: func(m string) string {
				return strings.Replace(strings.Replace(m, `"format":3`, `"format":2`, 1), `,`+kindKey, ``, 1)
			},
			wantErr: backup.ErrCorrupt,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			fix, id := writeKindManifest(t, tc.change)

			_, readErr := fix.service.ReadManifest(t.Context(), id)
			checkErr := backup.Check(t.Context(), fix.target, id)

			checkManifestError(t, tc.name, "read", readErr, tc.wantErr)
			checkManifestError(t, tc.name, "check", checkErr, tc.wantErr)
		})
	}
}

// TestExportRecordsTheKindOfItsOwnDatabase checks that the manifest holds the
// kind that the reader of the database gives (layer 1f), and that an export
// fails with ErrUnavailable and writes no manifest and no file when the
// database claims no kind or a value that is not a known kind.
func TestExportRecordsTheKindOfItsOwnDatabase(t *testing.T) {
	t.Parallel()

	sql := []byte("sqlite-image-bytes")

	for _, kind := range []installation.Kind{installation.KindDev, installation.KindProduction} {
		fix := newFixtureKind(t, &stubExporter{sum: testSummary(), data: sql, err: nil}, fixedKind(kind), 0)
		mustRun(t, fix.service, "bkp-ok")

		manifest, err := fix.service.ReadManifest(t.Context(), "bkp-ok")
		if err != nil || manifest.InstallationKind != kind {
			t.Fatalf("manifest kind = %q, %v, want %q", manifest.InstallationKind, err, kind)
		}
	}

	failing := []struct {
		name string
		read kindReaderFunc
	}{
		{name: "no claimed kind", read: func(context.Context) (installation.Kind, error) {
			return "", installation.ErrUnavailable
		}},
		{name: "unknown stored value", read: func(context.Context) (installation.Kind, error) {
			return "staging", nil
		}},
		{name: "empty value without an error", read: func(context.Context) (installation.Kind, error) {
			return "", nil
		}},
	}

	for _, tc := range failing {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			fix := newFixtureKind(t, &stubExporter{sum: testSummary(), data: sql, err: nil}, tc.read, 0)

			_, err := fix.service.Run(t.Context(), "bkp-none")
			if !errors.Is(err, installation.ErrUnavailable) {
				t.Fatalf("run: err = %v, want ErrUnavailable", err)
			}

			assertNoManifest(t, fix.target, "bkp-none")
			assertFailed(t, fix.registry, "bkp-none")

			if n := fix.target.count(); n != 0 {
				t.Fatalf("target holds %d objects after a refused export, want none", n)
			}
		})
	}
}

// checkManifestError proves that err is nil when want is nil, and else wraps
// want. A format error never reads as a kind error.
func checkManifestError(t *testing.T, name, step string, err, want error) {
	t.Helper()

	if want == nil {
		if err != nil {
			t.Fatalf("%s a manifest with the kind %s: %v", step, name, err)
		}

		return
	}

	if !errors.Is(err, want) {
		t.Fatalf("%s: err = %v, want %v", step, err, want)
	}

	if errors.Is(want, backup.ErrCorrupt) && errors.Is(err, backup.ErrManifestKindInvalid) {
		t.Fatalf("%s: a format 2 manifest got the kind error: %v", step, err)
	}
}
