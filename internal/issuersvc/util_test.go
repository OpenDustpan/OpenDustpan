package issuersvc_test

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"slices"
	"testing"

	"opendustpan.org/opendustpan/pkg/notify"
	"opendustpan.org/opendustpan/pkg/profile"
)

func jsonUnmarshal(raw []byte, into any) error {
	if err := json.Unmarshal(raw, into); err != nil {
		return fmt.Errorf("unmarshal: %w", err)
	}

	return nil
}

// jsonString encodes v. A value that does not encode gives the text of the error.
func jsonString(v any) string {
	raw, err := json.Marshal(v)
	if err != nil {
		return err.Error()
	}

	return string(raw)
}

func slicesContains(types []notify.Type, want notify.Type) bool { return slices.Contains(types, want) }

// mustJSON encodes v and fails the test when it does not encode.
func mustJSON(tb testing.TB, v any) []byte {
	tb.Helper()

	raw, err := json.Marshal(v)
	if err != nil {
		tb.Fatalf("encode: %v", err)
	}

	return raw
}

// mustB64 decodes base64url text and fails the test when it does not decode.
func mustB64(tb testing.TB, s string) []byte {
	tb.Helper()

	raw, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil {
		tb.Fatalf("decode %q: %v", s, err)
	}

	return raw
}

// num reads a number claim and fails the test when it is not a number.
func num(tb testing.TB, claims map[string]any, key string) float64 {
	tb.Helper()

	v, ok := claims[key].(float64)
	if !ok {
		tb.Fatalf("claim %s = %v, want a number", key, claims[key])
	}

	return v
}

// patchURL, patchTemplate, patchAllow and patchActor return a settings patch
// that names one setting.
func patchURL(v string) profile.SettingsPatch {
	return profile.SettingsPatch{IssuerURL: &v, SubjectTemplate: nil, AllowBlockSubjects: nil, IncludeActorName: nil}
}

func patchTemplate(v ...string) profile.SettingsPatch {
	return profile.SettingsPatch{IssuerURL: nil, SubjectTemplate: &v, AllowBlockSubjects: nil, IncludeActorName: nil}
}

func patchAllow(v bool) profile.SettingsPatch {
	return profile.SettingsPatch{IssuerURL: nil, SubjectTemplate: nil, AllowBlockSubjects: &v, IncludeActorName: nil}
}

func patchActor(v bool) profile.SettingsPatch {
	return profile.SettingsPatch{IssuerURL: nil, SubjectTemplate: nil, AllowBlockSubjects: nil, IncludeActorName: &v}
}
