package setting_test

import (
	"errors"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/pkg/setting"
	"opendustpan.org/opendustpan/pkg/setting/settingtest"
)

func TestParsePatch(t *testing.T) {
	t.Parallel()

	reg := settingtest.Registry()

	tests := []struct {
		name  string
		body  string
		level setting.Level
		want  setting.Patch
		err   error
	}{
		{
			name: "values of each kind and a delete", level: setting.LevelInstallation,
			body: `{"test.label":"blue","test.limit":7,"test.enabled":null}`,
			want: setting.Patch{
				{Key: settingtest.KeyEnabled, Value: "", Delete: true},
				{Key: settingtest.KeyLabel, Value: "blue", Delete: false},
				{Key: settingtest.KeyLimit, Value: "7", Delete: false},
			},
			err: nil,
		},
		{name: "an empty object", level: setting.LevelInstallation, body: `{}`, want: nil, err: nil},
		{name: "a namespace key", level: setting.LevelNamespace, body: `{"test.nsFlag":true}`, want: setting.Patch{
			{Key: settingtest.KeyNSFlag, Value: "true", Delete: false},
		}, err: nil},
		{
			name:  "a key of the other level",
			level: setting.LevelInstallation,
			body:  `{"test.nsFlag":true}`,
			want:  nil,
			err:   setting.ErrUnknownKey,
		},
		{
			name:  "an unknown key",
			level: setting.LevelInstallation,
			body:  `{"nope":1}`,
			want:  nil,
			err:   setting.ErrUnknownKey,
		},
		{
			name:  "a string for an int",
			level: setting.LevelInstallation,
			body:  `{"test.limit":"7"}`,
			want:  nil,
			err:   setting.ErrInvalidValue,
		},
		{
			name:  "a fraction for an int",
			level: setting.LevelInstallation,
			body:  `{"test.limit":7.5}`,
			want:  nil,
			err:   setting.ErrInvalidValue,
		},
		{
			name:  "an exponent for an int",
			level: setting.LevelInstallation,
			body:  `{"test.limit":1e2}`,
			want:  nil,
			err:   setting.ErrInvalidValue,
		},
		{
			name:  "a bool for a string",
			level: setting.LevelInstallation,
			body:  `{"test.label":true}`,
			want:  nil,
			err:   setting.ErrInvalidValue,
		},
		{
			name:  "a failed check",
			level: setting.LevelInstallation,
			body:  `{"test.limit":99999}`,
			want:  nil,
			err:   setting.ErrInvalidValue,
		},
		{
			name:  "an object value",
			level: setting.LevelInstallation,
			body:  `{"test.label":{"a":1}}`,
			want:  nil,
			err:   setting.ErrInvalidValue,
		},
		{
			name:  "an array value",
			level: setting.LevelInstallation,
			body:  `{"test.label":[1]}`,
			want:  nil,
			err:   setting.ErrInvalidValue,
		},
		{
			name:  "a duplicate key",
			level: setting.LevelInstallation,
			body:  `{"test.label":"a","test.label":"b"}`,
			want:  nil,
			err:   setting.ErrInvalidPatch,
		},
		{name: "an array body", level: setting.LevelInstallation, body: `[]`, want: nil, err: setting.ErrInvalidPatch},
		{name: "a null body", level: setting.LevelInstallation, body: `null`, want: nil, err: setting.ErrInvalidPatch},
		{name: "an empty body", level: setting.LevelInstallation, body: ``, want: nil, err: setting.ErrInvalidPatch},
		{
			name:  "data after the object",
			level: setting.LevelInstallation,
			body:  `{} {}`,
			want:  nil,
			err:   setting.ErrInvalidPatch,
		},
		{
			name:  "a broken object",
			level: setting.LevelInstallation,
			body:  `{"test.label":`,
			want:  nil,
			err:   setting.ErrInvalidPatch,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got, err := setting.ParsePatch([]byte(tc.body), reg, tc.level)
			if !errors.Is(err, tc.err) {
				t.Fatalf("error = %v, want %v", err, tc.err)
			}

			if len(got) != len(tc.want) {
				t.Fatalf("patch = %v, want %v", got, tc.want)
			}

			for i := range got {
				if got[i] != tc.want[i] {
					t.Fatalf("edit %d = %+v, want %+v", i, got[i], tc.want[i])
				}
			}
		})
	}
}

// TestParsePatchErrorsHoldNoValueAndNoHostileKey checks that an error does
// not echo the value of the request, and that a key that is not shaped like
// a key does not reach a log line.
func TestParsePatchErrorsHoldNoValueAndNoHostileKey(t *testing.T) {
	t.Parallel()

	reg := settingtest.Registry()

	_, err := setting.ParsePatch([]byte(`{"test.limit":"canary-value"}`), reg, setting.LevelInstallation)
	if err == nil || strings.Contains(err.Error(), "canary-value") {
		t.Fatalf("error = %v, want one with no value", err)
	}

	_, err = setting.ParsePatch([]byte(`{"a\nforged log line":1}`), reg, setting.LevelInstallation)
	if err == nil || strings.ContainsAny(err.Error(), "\r\n") || strings.Contains(err.Error(), "forged") {
		t.Fatalf("error = %q, want one with no key text", err)
	}
}

func TestPatchKeys(t *testing.T) {
	t.Parallel()

	patch := setting.Patch{{Key: "a", Value: "", Delete: true}, {Key: "b", Value: "1", Delete: false}}
	if got := strings.Join(patch.Keys(), ","); got != "a,b" {
		t.Fatalf("keys = %s", got)
	}
}
