package preferences

import (
	"fmt"
	"slices"
	"strings"
	"time"

	"opendustpan.org/opendustpan/internal/locale"
)

// fieldSpec describes one field that a patch can change: its flat name, its
// default (empty for a nullable field) and the check of a value.
type fieldSpec struct {
	name  string
	def   string
	check func(value string) error
}

const (
	maxSchemeLen   = 64
	maxTimeZoneLen = 64
)

// fieldSpecs lists the fields in the order of the document.
//
//nolint:gochecknoglobals // A frozen table of the fields, as backup.Tables is.
var fieldSpecs = []fieldSpec{
	{FieldTheme, defaultTheme, oneOf(FieldTheme, "default", "terminal")},
	{FieldColorMode, defaultColorMode, oneOf(FieldColorMode, "system", "light", "dark")},
	{FieldTerminalSchemeLight, defaultScheme, schemeID(FieldTerminalSchemeLight)},
	{FieldTerminalSchemeDark, defaultScheme, schemeID(FieldTerminalSchemeDark)},
	{FieldShortcutHints, defaultShortcutSetting, oneOf(FieldShortcutHints, "theme", "on", "off")},
	{FieldSingleKeyShortcuts, defaultShortcutSetting, oneOf(FieldSingleKeyShortcuts, "theme", "on", "off")},
	{FieldShortcutHintStyle, defaultShortcutHintStyle, oneOf(FieldShortcutHintStyle, "brackets", "angles", "inverse")},
	{FieldLanguage, "", languageTag(FieldLanguage)},
	{FieldFormattingLocale, "", formattingLocale},
	{FieldHourCycle, "", oneOf(FieldHourCycle, "h12", "h23")},
	{FieldDateStyle, defaultDateStyle, oneOf(FieldDateStyle, "locale", "iso8601", "dmy", "mdy")},
	{FieldFirstDayOfWeek, "", oneOf(FieldFirstDayOfWeek, "mon", "tue", "wed", "thu", "fri", "sat", "sun")},
	{FieldTimeZone, "", timeZone},
	{FieldDigits, "", digits},
	{FieldResolvedLanguage, "", languageTag(FieldResolvedLanguage)},
}

// specOf returns the spec of the field name.
func specOf(name string) (fieldSpec, bool) {
	for i := range fieldSpecs {
		if fieldSpecs[i].name == name {
			return fieldSpecs[i], true
		}
	}

	return fieldSpec{name: "", def: "", check: nil}, false
}

// checkValue runs the check of the field name on value. It returns
// ErrUnknownField for a name that is not a field, and a *FieldError for a
// value that is not allowed.
func checkValue(name, value string) error {
	spec, ok := specOf(name)
	if !ok {
		return ErrUnknownField
	}

	if err := spec.check(value); err != nil {
		return &FieldError{Field: name, Err: err}
	}

	return nil
}

// oneOf returns the check of a field with a closed list of values.
func oneOf(field string, allowed ...string) func(string) error {
	why := reason(fmt.Sprintf("%s must be %s", field, reasonList(allowed)))

	return func(value string) error {
		if slices.Contains(allowed, value) {
			return nil
		}

		return why
	}
}

// schemeID returns the check of the ID of a color scheme: 1 to 64 characters
// of a-z, 0-9 and a hyphen. The server checks the shape only, because the
// console owns the list of schemes.
func schemeID(field string) func(string) error {
	why := reason(field + " must be 1 to 64 characters of a-z, 0-9 and -")

	return func(value string) error {
		if value == "" || len(value) > maxSchemeLen {
			return why
		}

		for i := range len(value) {
			c := value[i]
			if (c < 'a' || c > 'z') && (c < '0' || c > '9') && c != '-' {
				return why
			}
		}

		return nil
	}
}

// named returns err, with the name of field in front when the text of err
// does not start with it. The result never holds the value that was checked.
func named(field string, err error) error {
	if strings.HasPrefix(err.Error(), field) {
		return err
	}

	return fmt.Errorf("%s: %w", field, err)
}

// languageTag returns the check of a field that holds a language tag. It
// answers the sentinel of the locale package and never the wrapped text, so
// the value of the request stays out of the answer.
func languageTag(field string) func(string) error {
	return func(value string) error {
		if err := locale.ValidateLanguage(value); err != nil {
			return named(field, locale.ErrInvalidLanguage)
		}

		return nil
	}
}

func formattingLocale(value string) error {
	if err := locale.ValidateFormattingLocale(value); err != nil {
		return named(FieldFormattingLocale, locale.ErrInvalidFormattingLocale)
	}

	return nil
}

func digits(value string) error {
	if _, err := locale.ParseDigits(value); err != nil {
		return locale.ErrInvalidDigits
	}

	return nil
}

var errTimeZone = reason("timeZone must be an IANA time zone name of 1 to 64 characters, for example Europe/Berlin")

// timeZone checks an IANA zone name. The check has two layers that each stop
// a hostile name alone: an allowlist of bytes and of segments, which runs
// before any file name is built, and the time zone database.
func timeZone(value string) error {
	if !zoneShape(value) {
		return errTimeZone
	}

	if _, err := time.LoadLocation(value); err != nil {
		return errTimeZone
	}

	return nil
}

// zoneShape reports whether value has the shape of an IANA zone name: 1 to 64
// bytes of ASCII letters, digits and the characters / _ + -, in segments with
// no empty segment. A zone name has no dot, so a segment of dots, a file
// extension and a relative path never pass. It also refuses some names that
// only some hosts have. The answer can still differ by host: time.LoadLocation
// reads the zone files of the host before the embedded database, so a host can
// know a name that the embedded database does not, and a file system that
// ignores case can accept a name in the wrong case.
func zoneShape(value string) bool {
	if value == "" || len(value) > maxTimeZoneLen {
		return false
	}

	for i := range len(value) {
		if !zoneByte(value[i]) {
			return false
		}
	}

	if strings.HasPrefix(value, "/") || strings.HasSuffix(value, "/") || strings.Contains(value, "//") {
		return false
	}

	// "Local" names the zone of the host, and the other names are files that
	// some hosts have and the embedded database does not.
	first, _, _ := strings.Cut(value, "/")
	switch strings.ToLower(first) {
	case "local", "localtime", "posixrules", "posix", "right":
		return false
	default:
		return true
	}
}

// zoneByte reports whether c can be in the name of a zone.
func zoneByte(c byte) bool {
	return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' ||
		c == '/' || c == '_' || c == '+' || c == '-'
}
