package preferences_test

import (
	"strings"
	"testing"

	"opendustpan.org/opendustpan/pkg/preferences"
)

func TestTimeZoneNames(t *testing.T) {
	t.Parallel()

	for _, name := range []string{
		"UTC", "GMT", "Europe/Berlin", "Asia/Riyadh", "Asia/Kolkata", "Pacific/Auckland", "Etc/GMT+5", "Etc/GMT-14",
		"America/Argentina/Buenos_Aires", "America/Port-au-Prince", "Australia/Lord_Howe", "EST5EDT",
	} {
		if _, err := preferences.ParsePatch([]byte(`{"timeZone":"` + name + `"}`)); err != nil {
			t.Errorf("zone %q: %v, want it accepted", name, err)
		}
	}

	for name, zone := range map[string]string{
		"empty":                  "",
		"a space":                " ",
		"a trailing space":       "UTC ",
		"a lowercase name":       "europe/berlin",
		"a path up":              "../etc/passwd",
		"a path up inside":       "Europe/../UTC",
		"a path from the root":   "/etc/localtime",
		"a dot":                  ".",
		"two dots":               "..",
		"an empty segment":       "Europe//Berlin",
		"a trailing slash":       "Europe/Berlin/",
		"a backslash":            `Europe\Berlin`,
		"a file extension":       "zone.tab",
		"the file of the host":   "localtime",
		"the zone of the host":   "Local",
		"the zone of the host 2": "LOCAL",
		"a host file":            "posixrules",
		"a posix directory":      "posix/Europe/Berlin",
		"a right directory":      "right/Europe/Berlin",
		"a zone that is not one": "Not/AZone",
		"a directory":            "America",
		"a null byte":            "UTC\x00",
		"a new line":             "UTC\n",
		"a percent escape":       "Europe%2FBerlin",
		"a non-ASCII letter":     "Europe/Zürich",
		"an emoji":               "Europe/🇩🇪",
		"over 64 bytes":          strings.Repeat("a", 65),
		"64 bytes of letters":    strings.Repeat("a", 64),
	} {
		_, err := preferences.ParsePatch([]byte(`{"timeZone":` + jsonText(zone) + `}`))
		if err == nil {
			t.Errorf("%s: zone %q accepted, want it refused", name, zone)
		}
	}
}

// jsonText quotes s as a JSON string.
func jsonText(s string) string {
	var b strings.Builder

	b.WriteByte('"')

	for _, r := range s {
		switch {
		case r == '"' || r == '\\':
			b.WriteByte('\\')
			b.WriteRune(r)
		case r < 0x20:
			b.WriteString(`\u00`)
			b.WriteByte("0123456789abcdef"[r>>4])
			b.WriteByte("0123456789abcdef"[r&0xf])
		default:
			b.WriteRune(r)
		}
	}

	b.WriteByte('"')

	return b.String()
}

func TestSchemeIDs(t *testing.T) {
	t.Parallel()

	for _, id := range []string{"a", "opendustpan", "solarized-light", "tokyo-night", "a1-b2", strings.Repeat("a", 64), "-", "0"} {
		if _, err := preferences.ParsePatch([]byte(`{"terminalSchemes":{"light":"` + id + `"}}`)); err != nil {
			t.Errorf("scheme %q: %v, want it accepted", id, err)
		}
	}

	for _, id := range []string{"", " ", "A", "Solarized", "a_b", "a b", "a.b", "a/b", "../x", "é", "a\n", "<b>", strings.Repeat("a", 65)} {
		if _, err := preferences.ParsePatch([]byte(`{"terminalSchemes":{"dark":` + jsonText(id) + `}}`)); err == nil {
			t.Errorf("scheme %q accepted, want it refused", id)
		}
	}
}

func TestLanguageTags(t *testing.T) {
	t.Parallel()

	for _, tag := range []string{"en", "ar", "he", "uk", "ru", "af", "zh-Hant-TW", "ar-EG", "sr-Latn-RS", "xx", "en-US-x-twain"} {
		if _, err := preferences.ParsePatch([]byte(`{"language":"` + tag + `"}`)); err != nil {
			t.Errorf("language %q: %v, want it accepted", tag, err)
		}
	}

	for _, tag := range []string{"", "e", "en_US", "en US", "-en", "en-", "en--US", "日本語", "en\n", strings.Repeat("a", 36), "ar-١"} {
		if _, err := preferences.ParsePatch([]byte(`{"language":` + jsonText(tag) + `}`)); err == nil {
			t.Errorf("language %q accepted, want it refused", tag)
		}
	}

	// A formatting locale has the same shape and takes no extension.
	for _, tag := range []string{"ar-u-nu-latn", "en-U-hc-h23", "en-x-private", "x-private", ""} {
		_, err := preferences.ParsePatch([]byte(`{"formattingLocale":` + jsonText(tag) + `}`))
		if err == nil {
			t.Errorf("formattingLocale %q accepted, want it refused", tag)
		}
	}
}
