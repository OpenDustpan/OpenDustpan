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

// hostileZones are names that each layer of the time zone check must refuse on
// its own, or that the layers below it must refuse. The case lists the layers
// that are meant to refuse the name.
type zoneCase struct {
	name      string
	zone      string
	allowlist bool // the allowlist refuses it
	shape     bool // the shape check refuses it
	loader    bool // the zone database refuses it
}

func hostileZones() []zoneCase {
	return []zoneCase{
		{"wrong case", "europe/berlin", true, false, false},
		{"wrong case in the second segment", "Europe/berlin", true, false, false},
		{"all upper case", "EUROPE/BERLIN", true, false, false},
		{"a trailing space", "Europe/Berlin ", true, true, true},
		{"a path up", "../etc/passwd", true, true, true},
		{"a path up inside", "Europe/Berlin/../Paris", true, true, true},
		{"a null byte", "Europe/Berlin\x00", true, true, true},
		{"a very long name", strings.Repeat("Europe/", 1000), true, true, true},
		{"the zone of the host", "Local", true, true, true},
		{"empty", "", true, true, true},
		{"the file of the host", "localtime", true, true, true},
		{"a host rules file", "posixrules", true, true, true},
		{"a posix directory", "posix/UTC", true, true, true},
		{"a right directory", "right/UTC", true, true, true},
		{"a directory", "America", true, false, false},
	}
}

// TestZoneAllowlist sends hostile names to the allowlist alone. The answer
// comes from a list in the binary, so it is the same on every host.
func TestZoneAllowlist(t *testing.T) {
	t.Parallel()

	for _, tc := range hostileZones() {
		if tc.allowlist && preferences.AllowlistAccepts(tc.zone) {
			t.Errorf("%s: allowlist accepted %q", tc.name, tc.zone)
		}
	}

	for _, zone := range []string{"UTC", "Europe/Berlin", "America/Argentina/Buenos_Aires", "Etc/GMT+5"} {
		if !preferences.AllowlistAccepts(zone) {
			t.Errorf("allowlist refused %q", zone)
		}
	}
}

// TestZoneShape sends hostile names to the shape check alone.
func TestZoneShape(t *testing.T) {
	t.Parallel()

	for _, tc := range hostileZones() {
		if tc.shape && preferences.ShapeAccepts(tc.zone) {
			t.Errorf("%s: shape check accepted %q", tc.name, tc.zone)
		}
	}

	if !preferences.ShapeAccepts("Europe/Berlin") {
		t.Error("shape check refused Europe/Berlin")
	}
}

// TestZoneLoads sends hostile names to the time zone database alone.
func TestZoneLoads(t *testing.T) {
	t.Parallel()

	for _, tc := range hostileZones() {
		if tc.loader && preferences.DatabaseAccepts(tc.zone) {
			t.Errorf("%s: zone database accepted %q", tc.name, tc.zone)
		}
	}

	for _, zone := range []string{"Not/AZone", "zone.tab"} {
		if preferences.DatabaseAccepts(zone) {
			t.Errorf("zone database accepted %q", zone)
		}
	}

	if !preferences.DatabaseAccepts("Europe/Berlin") {
		t.Error("zone database refused Europe/Berlin")
	}
}

// TestZonePatchRefusesWrongCase checks the public path. The refusal of a name
// in the wrong case comes from the allowlist, not from the host: the allowlist
// refuses it, so the answer is the same where the file system ignores case.
func TestZonePatchRefusesWrongCase(t *testing.T) {
	t.Parallel()

	for _, tc := range hostileZones() {
		if _, err := preferences.ParsePatch([]byte(`{"timeZone":` + jsonText(tc.zone) + `}`)); err == nil {
			t.Errorf("%s: patch accepted %q", tc.name, tc.zone)
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
