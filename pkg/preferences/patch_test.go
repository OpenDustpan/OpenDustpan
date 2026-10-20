package preferences_test

import (
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/locale"
	"opendustpan.org/opendustpan/pkg/preferences"
)

func TestParsePatch(t *testing.T) {
	t.Parallel()

	str := preferences.Text

	tests := []struct {
		name string
		body string
		want preferences.Patch
	}{
		{name: "an empty object", body: `{}`, want: preferences.Patch{}},
		{
			name: "whitespace around",
			body: " \n{ \"theme\" : \"terminal\" }\n",
			want: preferences.Patch{"theme": str("terminal")},
		},
		{name: "a set field", body: `{"language":"ar"}`, want: preferences.Patch{"language": str("ar")}},
		{
			name: "null resets",
			body: `{"language":null,"theme":null}`,
			want: preferences.Patch{"language": nil, "theme": nil},
		},
		{
			name: "null on terminalSchemes resets both",
			body: `{"terminalSchemes":null}`,
			want: preferences.Patch{"terminalSchemes.light": nil, "terminalSchemes.dark": nil},
		},
		{
			name: "an object of schemes changes the named ones",
			body: `{"terminalSchemes":{"light":"solarized-light","dark":null}}`,
			want: preferences.Patch{"terminalSchemes.light": str("solarized-light"), "terminalSchemes.dark": nil},
		},
		{name: "an empty object of schemes", body: `{"terminalSchemes":{}}`, want: preferences.Patch{}},
		{
			name: "every field",
			body: `{"theme":"terminal","colorMode":"dark","shortcutHints":"on","singleKeyShortcuts":"off",` +
				`"shortcutHintStyle":"angles","language":"he","formattingLocale":"he-IL","hourCycle":"h23",` +
				`"dateStyle":"dmy","firstDayOfWeek":"sun","timeZone":"Asia/Jerusalem","digits":"arab",` +
				`"resolvedLanguage":"he"}`,
			want: preferences.Patch{
				"theme": str("terminal"), "colorMode": str("dark"), "shortcutHints": str("on"),
				"singleKeyShortcuts": str("off"), "shortcutHintStyle": str("angles"), "language": str("he"),
				"formattingLocale": str("he-IL"), "hourCycle": str("h23"), "dateStyle": str("dmy"),
				"firstDayOfWeek": str("sun"), "timeZone": str("Asia/Jerusalem"), "digits": str("arab"),
				"resolvedLanguage": str("he"),
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got, err := preferences.ParsePatch([]byte(tc.body))
			if err != nil {
				t.Fatalf("ParsePatch(%s): %v", tc.body, err)
			}

			if fmt.Sprint(describe(got)) != fmt.Sprint(describe(tc.want)) {
				t.Errorf("ParsePatch(%s) = %v, want %v", tc.body, describe(got), describe(tc.want))
			}
		})
	}
}

// describe renders a patch for a comparison.
func describe(p preferences.Patch) map[string]string {
	out := map[string]string{}

	for k, v := range p {
		if v == nil {
			out[k] = "<null>"
		} else {
			out[k] = "=" + *v
		}
	}

	return out
}

func TestParsePatchRefusals(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		body string
		want error
	}{
		{name: "an empty body", body: ``, want: preferences.ErrInvalidBody},
		{name: "not JSON", body: `{`, want: preferences.ErrInvalidBody},
		{name: "an array", body: `[]`, want: preferences.ErrInvalidBody},
		{name: "a string", body: `"theme"`, want: preferences.ErrInvalidBody},
		{name: "null", body: `null`, want: preferences.ErrInvalidBody},
		{name: "a number", body: `1`, want: preferences.ErrInvalidBody},
		{name: "text after the object", body: `{} {}`, want: preferences.ErrInvalidBody},
		{name: "a second object", body: `{}{"theme":"terminal"}`, want: preferences.ErrInvalidBody},
		{name: "a repeated field", body: `{"language":"en","language":"fr"}`, want: preferences.ErrInvalidBody},
		{
			name: "a repeated field in schemes",
			body: `{"terminalSchemes":{"light":"a","light":"b"}}`,
			want: preferences.ErrInvalidValue,
		},
		{name: "a trailing comma", body: `{"theme":"terminal",}`, want: preferences.ErrInvalidBody},
		{name: "updatedAt", body: `{"updatedAt":1}`, want: preferences.ErrReadOnlyField},
		{name: "updatedAt null", body: `{"updatedAt":null}`, want: preferences.ErrReadOnlyField},
		{name: "updatedAt with a bad field", body: `{"nope":1,"updatedAt":1}`, want: preferences.ErrReadOnlyField},
		{name: "an unknown field", body: `{"nope":"x"}`, want: preferences.ErrUnknownField},
		{name: "a field with another case", body: `{"Theme":"terminal"}`, want: preferences.ErrUnknownField},
		{name: "a flat scheme name", body: `{"terminalSchemes.light":"a"}`, want: preferences.ErrUnknownField},
		{name: "an unknown scheme", body: `{"terminalSchemes":{"mid":"a"}}`, want: preferences.ErrUnknownField},
		{name: "schemes as a string", body: `{"terminalSchemes":"opendustpan"}`, want: preferences.ErrInvalidValue},
		{name: "schemes as an array", body: `{"terminalSchemes":[]}`, want: preferences.ErrInvalidValue},
		{name: "schemes as a number", body: `{"terminalSchemes":1}`, want: preferences.ErrInvalidValue},
		{name: "a number value", body: `{"theme":1}`, want: preferences.ErrInvalidValue},
		{name: "a boolean value", body: `{"shortcutHints":true}`, want: preferences.ErrInvalidValue},
		{name: "an object value", body: `{"language":{}}`, want: preferences.ErrInvalidValue},
		{name: "an array value", body: `{"language":["en"]}`, want: preferences.ErrInvalidValue},
		{name: "a value that is not allowed", body: `{"theme":"dark"}`, want: preferences.ErrInvalidValue},
		{name: "an empty text", body: `{"language":""}`, want: preferences.ErrInvalidValue},
		{
			name: "a scheme value that is a number",
			body: `{"terminalSchemes":{"light":1}}`,
			want: preferences.ErrInvalidValue,
		},
		{
			name: "a bad scheme ID",
			body: `{"terminalSchemes":{"dark":"Tokyo Night"}}`,
			want: preferences.ErrInvalidValue,
		},
		{name: "digits that are not allowed", body: `{"digits":"deva"}`, want: locale.ErrInvalidDigits},
		{name: "a bad language", body: `{"language":"ar_EG"}`, want: locale.ErrInvalidLanguage},
		{name: "a bad resolved language", body: `{"resolvedLanguage":"ar EG"}`, want: locale.ErrInvalidLanguage},
		{
			name: "an extension in the formatting locale",
			body: `{"formattingLocale":"ar-u-nu-latn"}`,
			want: locale.ErrInvalidFormattingLocale,
		},
		{name: "a bad zone", body: `{"timeZone":"../../etc/passwd"}`, want: preferences.ErrInvalidValue},
		{
			name: "a body over the limit",
			body: `{"language":"` + strings.Repeat("a", preferences.MaxBodyBytes) + `"}`,
			want: preferences.ErrBodyTooLarge,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got, err := preferences.ParsePatch([]byte(tc.body))
			if !errors.Is(err, tc.want) {
				t.Fatalf("ParsePatch(%.60s) error = %v, want %v", tc.body, err, tc.want)
			}

			if got != nil {
				t.Errorf("ParsePatch returned a patch %v with an error", got)
			}
		})
	}
}

// An error text is fixed and holds no value from the request.
func TestErrorsHoldNoRequestValue(t *testing.T) {
	t.Parallel()

	for _, body := range []string{
		`{"Zz9-secret-field":"x"}`,
		`{"language":"Zz9-secret-value"}`,
		`{"theme":"Zz9-secret-value"}`,
		`{"digits":"Zz9-secret-value"}`,
		`{"timeZone":"Zz9-secret-value"}`,
		`{"formattingLocale":"Zz9-secret-value-u-x"}`,
		`{"terminalSchemes":{"Zz9-secret-field":"x"}}`,
		`{"terminalSchemes":{"light":"Zz9_secret"}}`,
	} {
		_, err := preferences.ParsePatch([]byte(body))
		if err == nil {
			t.Fatalf("ParsePatch(%s) = nil, want an error", body)
		}

		if strings.Contains(strings.ToLower(err.Error()), "zz9") {
			t.Errorf("ParsePatch(%s) error %q holds the request text", body, err)
		}
	}
}

func TestDigitsErrorNamesTheValidValues(t *testing.T) {
	t.Parallel()

	_, err := preferences.ParsePatch([]byte(`{"digits":"deva"}`))
	if err == nil {
		t.Fatal("ParsePatch = nil, want an error")
	}

	for _, want := range []string{"latn", "arab", "null"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not name %q", err, want)
		}
	}

	var fieldErr *preferences.FieldError
	if !errors.As(err, &fieldErr) || fieldErr.Field != preferences.FieldDigits {
		t.Errorf("error = %v, want a field error for digits", err)
	}
}

func TestValidateRefusesWhatTheRouteRefuses(t *testing.T) {
	t.Parallel()

	// Validate is a layer of its own: a patch that code builds meets the same
	// checks.
	for name, patch := range map[string]preferences.Patch{
		"updatedAt":       {"updatedAt": preferences.Text("1")},
		"terminalSchemes": {"terminalSchemes": nil},
		"unknown":         {"x": nil},
		"bad value":       {"hourCycle": preferences.Text("h99")},
		"empty value":     {"theme": preferences.Text("")},
	} {
		if err := patch.Validate(); err == nil {
			t.Errorf("%s: Validate = nil, want an error", name)
		}
	}

	if err := (preferences.Patch{"hourCycle": nil, "theme": preferences.Text("terminal")}).Validate(); err != nil {
		t.Errorf("Validate of a good patch = %v", err)
	}
}

func TestApply(t *testing.T) {
	t.Parallel()

	str := preferences.Text

	tests := []struct {
		name        string
		current     preferences.Settings
		patch       preferences.Patch
		want        preferences.Settings
		wantChanged []string
	}{
		{
			name:        "nothing",
			current:     nil,
			patch:       preferences.Patch{},
			want:        preferences.Settings{},
			wantChanged: []string{},
		},
		{
			name:        "set a field",
			current:     nil,
			patch:       preferences.Patch{"language": str("ar")},
			want:        preferences.Settings{"language": "ar"},
			wantChanged: []string{"language"},
		},
		{
			name:        "set the same value",
			current:     preferences.Settings{"language": "ar"},
			patch:       preferences.Patch{"language": str("ar")},
			want:        preferences.Settings{"language": "ar"},
			wantChanged: []string{},
		},
		{
			name:        "set the default of a field with a default",
			current:     nil,
			patch:       preferences.Patch{"theme": str("default"), "dateStyle": str("locale")},
			want:        preferences.Settings{},
			wantChanged: []string{},
		},
		{
			name:        "set the default over a value",
			current:     preferences.Settings{"theme": "terminal"},
			patch:       preferences.Patch{"theme": str("default")},
			want:        preferences.Settings{},
			wantChanged: []string{"theme"},
		},
		{
			name:        "null on a field with no value",
			current:     preferences.Settings{"language": "ar"},
			patch:       preferences.Patch{"theme": nil, "digits": nil},
			want:        preferences.Settings{"language": "ar"},
			wantChanged: []string{},
		},
		{
			name:        "null on a value",
			current:     preferences.Settings{"language": "ar", "digits": "latn"},
			patch:       preferences.Patch{"digits": nil},
			want:        preferences.Settings{"language": "ar"},
			wantChanged: []string{"digits"},
		},
		{
			name:    "the changed fields are in the order of the document",
			current: nil,
			patch: preferences.Patch{
				"digits":               str("arab"),
				"theme":                str("terminal"),
				"terminalSchemes.dark": str("nord"),
			},
			want:        preferences.Settings{"digits": "arab", "theme": "terminal", "terminalSchemes.dark": "nord"},
			wantChanged: []string{"theme", "terminalSchemes.dark", "digits"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got, changed, err := preferences.Apply(tc.current, tc.patch)
			if err != nil {
				t.Fatalf("Apply: %v", err)
			}

			if fmt.Sprint(got) != fmt.Sprint(tc.want) {
				t.Errorf("settings = %v, want %v", got, tc.want)
			}

			if fmt.Sprint(changed) != fmt.Sprint(tc.wantChanged) {
				t.Errorf("changed = %v, want %v", changed, tc.wantChanged)
			}
		})
	}
}

func TestApplyChangesNothingOnAnError(t *testing.T) {
	t.Parallel()

	current := preferences.Settings{"language": "ar"}
	patch := preferences.Patch{"theme": preferences.Text("terminal"), "digits": preferences.Text("deva")}

	if _, _, err := preferences.Apply(current, patch); !errors.Is(err, locale.ErrInvalidDigits) {
		t.Fatalf("Apply error = %v, want ErrInvalidDigits", err)
	}

	if fmt.Sprint(current) != "map[language:ar]" {
		t.Errorf("current = %v, want it unchanged", current)
	}
}

// A stored value that fails a check never reaches a write: Apply checks the
// new settings. A patch that sets the field back to its default always works.
func TestApplyChecksTheNewSettings(t *testing.T) {
	t.Parallel()

	for _, current := range []preferences.Settings{
		{"language": "ar_EG"},
		{"nope": "x"},
		{"digits": "deva"},
		{"timeZone": "../x"},
		{"terminalSchemes": "x"},
	} {
		if _, _, err := preferences.Apply(current, preferences.Patch{}); !errors.Is(err, preferences.ErrInvalidStored) {
			t.Errorf("Apply(%v) error = %v, want ErrInvalidStored", current, err)
		}
	}
}

func TestApplyResetsAStoredFieldThatFailsACheck(t *testing.T) {
	t.Parallel()

	current := preferences.Settings{"timeZone": "Mars/Olympus", "theme": "terminal"}

	for name, patch := range map[string]preferences.Patch{
		"null":  {"timeZone": nil},
		"other": {"timeZone": preferences.Text("Europe/Berlin")},
	} {
		next, changed, err := preferences.Apply(current, patch)
		if err != nil || !slices.Contains(changed, "timeZone") {
			t.Errorf("%s: Apply = %v, %v, %v; want a change of timeZone", name, next, changed, err)
		}
	}

	if _, _, err := preferences.Apply(current, preferences.Patch{"theme": preferences.Text("default")}); !errors.Is(
		err, preferences.ErrInvalidStored,
	) {
		t.Errorf("a patch that keeps the bad field: error = %v, want ErrInvalidStored", err)
	}
}

func TestParseStoredDropsWhatFailsACheck(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		text    string
		want    preferences.Settings
		dropped []string
	}{
		{
			"a bad zone",
			`{"timeZone":"Mars/Olympus","theme":"terminal"}`,
			preferences.Settings{"theme": "terminal"},
			[]string{"timeZone"},
		},
		{"an unknown member", `{"nope":"x","language":"he"}`, preferences.Settings{"language": "he"}, []string{"nope"}},
		{"a value that is not text", `{"theme":3}`, preferences.Settings{}, []string{"theme"}},
		{"not an object", `[1]`, preferences.Settings{}, []string{"document"}},
		{"a repeated member", `{"theme":"terminal","theme":"default"}`, preferences.Settings{}, []string{"document"}},
		{"all good", `{"theme":"terminal"}`, preferences.Settings{"theme": "terminal"}, []string{}},
	}

	for _, tc := range tests {
		got, dropped := preferences.ParseStored(tc.text)
		if !maps.Equal(got, tc.want) || !slices.Equal(dropped, tc.dropped) {
			t.Errorf("%s: ParseStored = %v, %v; want %v, %v", tc.name, got, dropped, tc.want, tc.dropped)
		}
	}
}

func TestSettingsEncodeParse(t *testing.T) {
	t.Parallel()

	s := preferences.Settings{"theme": "terminal", "language": "he", "terminalSchemes.dark": "nord"}

	text, err := s.Encode()
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}

	const want = `{"language":"he","terminalSchemes.dark":"nord","theme":"terminal"}`
	if text != want {
		t.Errorf("Encode = %s, want %s", text, want)
	}

	back, err := preferences.ParseSettings(text)
	if err != nil {
		t.Fatalf("ParseSettings: %v", err)
	}

	if fmt.Sprint(back) != fmt.Sprint(s) {
		t.Errorf("ParseSettings = %v, want %v", back, s)
	}

	if _, err := (preferences.Settings{"language": "bad_tag"}).Encode(); !errors.Is(err, preferences.ErrInvalidStored) {
		t.Errorf("Encode of a bad value error = %v, want ErrInvalidStored", err)
	}
}

func TestParseSettingsRefusals(t *testing.T) {
	t.Parallel()

	for name, text := range map[string]string{
		"empty":            ``,
		"not an object":    `[]`,
		"null":             `null`,
		"a repeated key":   `{"language":"en","language":"fr"}`,
		"an unknown key":   `{"nope":"x"}`,
		"updatedAt":        `{"updatedAt":"1"}`,
		"the nested name":  `{"terminalSchemes":{"light":"a"}}`,
		"a number":         `{"language":1}`,
		"a null":           `{"language":null}`,
		"a bad value":      `{"language":"en us"}`,
		"digits":           `{"digits":"deva"}`,
		"a zone":           `{"timeZone":"Local"}`,
		"too long":         `{"language":"` + strings.Repeat("a", preferences.MaxBodyBytes) + `"}`,
		"text after":       `{} x`,
		"a hidden control": "{\"language\":\"en" + string(rune(0x202e)) + "\"}",
	} {
		if _, err := preferences.ParseSettings(text); !errors.Is(err, preferences.ErrInvalidStored) {
			t.Errorf("%s: ParseSettings error = %v, want ErrInvalidStored", name, err)
		}
	}

	got, err := preferences.ParseSettings(`{"theme":"default","language":"he"}`)
	if err != nil || fmt.Sprint(got) != "map[language:he]" {
		t.Errorf("ParseSettings with a default value = %v, %v; want the default dropped", got, err)
	}
}

func TestSettingsDocument(t *testing.T) {
	t.Parallel()

	if got := preferences.Settings(nil).Document(); got != preferences.Default() {
		t.Errorf("Document of no settings = %+v, want the defaults", got)
	}

	all := preferences.Settings{}
	for _, name := range preferences.Fields() {
		all[name] = "x"
	}

	doc := all.Document()
	for _, got := range []string{
		doc.Theme, doc.ColorMode, doc.TerminalSchemes.Light, doc.TerminalSchemes.Dark, doc.ShortcutHints,
		doc.SingleKeyShortcuts, doc.ShortcutHintStyle, doc.Language, doc.FormattingLocale, doc.HourCycle,
		doc.DateStyle, doc.FirstDayOfWeek, doc.TimeZone, doc.Digits, doc.ResolvedLanguage,
	} {
		if got != "x" {
			t.Errorf("Document = %+v, want every field set from its stored value", doc)

			break
		}
	}
}

func TestDefaults(t *testing.T) {
	t.Parallel()

	want := preferences.Document{
		UpdatedAt:          time.Time{},
		Theme:              "default",
		ColorMode:          "system",
		TerminalSchemes:    preferences.TerminalSchemes{Light: "opendustpan", Dark: "opendustpan"},
		ShortcutHints:      "theme",
		SingleKeyShortcuts: "theme",
		ShortcutHintStyle:  "brackets",
		Language:           "",
		FormattingLocale:   "",
		HourCycle:          "",
		DateStyle:          "locale",
		FirstDayOfWeek:     "",
		TimeZone:           "",
		Digits:             "",
		ResolvedLanguage:   "",
	}

	if got := preferences.Default(); got != want {
		t.Errorf("Default = %+v, want %+v", got, want)
	}
}
