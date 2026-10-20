// Package preferences keeps the preferences of a person: the settings of the
// console that follow the person to every device (theme, color mode, color
// schemes, keyboard settings, language, formats, time zone and digits).
//
// The package is tool-neutral. It holds the document, the checks of each
// field, the merge patch and the port of the store. A preference belongs to a
// person, not to a namespace, so no write carries a fence.
//
// The server stores only the settings that a person changed. A field that a
// person never set, or set back with null, has no stored value and reads as
// its default. A language or a scheme is checked for its shape only: the
// console owns the list of languages and of schemes.
package preferences

import (
	"errors"
	"slices"
	"strings"
	"time"
	// The checks of timeZone read the embedded time zone database, so an
	// installation on a host with no zone files accepts the same names.
	_ "time/tzdata"
)

// The names of the fields on the wire. The nested names of the color schemes
// are flat here and in the audit entry, with a dot.
const (
	FieldTheme               = "theme"
	FieldColorMode           = "colorMode"
	FieldTerminalSchemes     = "terminalSchemes"
	FieldTerminalSchemeLight = "terminalSchemes.light"
	FieldTerminalSchemeDark  = "terminalSchemes.dark"
	FieldShortcutHints       = "shortcutHints"
	FieldSingleKeyShortcuts  = "singleKeyShortcuts"
	FieldShortcutHintStyle   = "shortcutHintStyle"
	FieldLanguage            = "language"
	FieldFormattingLocale    = "formattingLocale"
	FieldHourCycle           = "hourCycle"
	FieldDateStyle           = "dateStyle"
	FieldFirstDayOfWeek      = "firstDayOfWeek"
	FieldTimeZone            = "timeZone"
	FieldDigits              = "digits"
	FieldResolvedLanguage    = "resolvedLanguage"
	// FieldUpdatedAt is read-only: the store sets it.
	FieldUpdatedAt = "updatedAt"
)

// MaxBodyBytes is the largest body of a patch, in bytes.
const MaxBodyBytes = 4 << 10

// AuditAction is the action of the audit entry that a change writes.
const AuditAction = "preferences-changed"

// Sentinel errors. The text of each error is fixed and holds no value from a
// request.
var (
	// ErrInvalidBody means a patch is not one JSON object, or it names a
	// field two times.
	ErrInvalidBody = errors.New("body must be one JSON object that names no field two times")
	// ErrBodyTooLarge means a patch is longer than MaxBodyBytes.
	ErrBodyTooLarge = errors.New("body must be 4096 bytes or fewer")
	// ErrUnknownField means a patch names a field that the document does not
	// have. The error never holds the name.
	ErrUnknownField = errors.New("body has a field that preferences do not have")
	// ErrReadOnlyField means a patch holds updatedAt.
	ErrReadOnlyField = errors.New("updatedAt is read-only")
	// ErrInvalidValue means the value of a field is not allowed. A
	// *FieldError wraps it and names the field.
	ErrInvalidValue = errors.New("preference value is not valid")
	// ErrPersonNotFound means no person exists with the ID: it never
	// existed, or it was deleted, or its record is not a person.
	ErrPersonNotFound = errors.New("person not found")
	// ErrNotPerson means the caller is not a person with a record: a service,
	// a workload, a job or a bootstrap administrator.
	ErrNotPerson = errors.New("preferences belong to a person, and the caller is not a person with a record")
	// ErrInvalidStored means a stored document is not valid. A store returns
	// it for a row that fails the checks, and a restore refuses the row.
	ErrInvalidStored = errors.New("stored preferences are not valid")
)

// reason is the fixed text of a check. A value of the type is an error whose
// text holds the name of its field and never a value of the request.
type reason string

func (r reason) Error() string { return string(r) }

// FieldError names the field of a value that is not allowed. Field is always
// a name from this package, never text from a request. It matches
// ErrInvalidValue and the error of the check.
type FieldError struct {
	Field string
	Err   error
}

// Error returns the text of the check. Each check names its field.
func (e *FieldError) Error() string { return e.Err.Error() }

// Unwrap returns ErrInvalidValue and the error of the check.
func (e *FieldError) Unwrap() []error { return []error{ErrInvalidValue, e.Err} }

// TerminalSchemes holds the color scheme of each color mode of the Terminal
// theme.
type TerminalSchemes struct {
	Light string
	Dark  string
}

// Document is the full preferences of one person. A field of text type that
// is empty is null on the wire: a nullable field with no value. UpdatedAt is
// the zero time for a person that never changed a preference.
type Document struct {
	UpdatedAt          time.Time
	Theme              string
	ColorMode          string
	TerminalSchemes    TerminalSchemes
	ShortcutHints      string
	SingleKeyShortcuts string
	ShortcutHintStyle  string
	Language           string
	FormattingLocale   string
	HourCycle          string
	DateStyle          string
	FirstDayOfWeek     string
	TimeZone           string
	Digits             string
	ResolvedLanguage   string
}

// The defaults of the fields that have one. A nullable field has the empty
// default.
const (
	defaultTheme             = "default"
	defaultColorMode         = "system"
	defaultScheme            = "opendustpan"
	defaultShortcutSetting   = "theme"
	defaultShortcutHintStyle = "brackets"
	defaultDateStyle         = "locale"
)

// Default returns the document of a person that never changed a preference.
func Default() Document {
	return Document{
		UpdatedAt:          time.Time{},
		Theme:              defaultTheme,
		ColorMode:          defaultColorMode,
		TerminalSchemes:    TerminalSchemes{Light: defaultScheme, Dark: defaultScheme},
		ShortcutHints:      defaultShortcutSetting,
		SingleKeyShortcuts: defaultShortcutSetting,
		ShortcutHintStyle:  defaultShortcutHintStyle,
		Language:           "",
		FormattingLocale:   "",
		HourCycle:          "",
		DateStyle:          defaultDateStyle,
		FirstDayOfWeek:     "",
		TimeZone:           "",
		Digits:             "",
		ResolvedLanguage:   "",
	}
}

// Settings holds the preferences that a person changed, by flat field name.
// A field that is not in the map has its default. A value in the map is
// never the default of its field, so two Settings are equal exactly when
// they give the same document.
type Settings map[string]string

// Document returns the document that s gives: the stored value of each field
// that s holds, and the default of every other field. The caller sets
// UpdatedAt.
func (s Settings) Document() Document {
	d := Default()

	set := map[string]*string{
		FieldTheme:               &d.Theme,
		FieldColorMode:           &d.ColorMode,
		FieldTerminalSchemeLight: &d.TerminalSchemes.Light,
		FieldTerminalSchemeDark:  &d.TerminalSchemes.Dark,
		FieldShortcutHints:       &d.ShortcutHints,
		FieldSingleKeyShortcuts:  &d.SingleKeyShortcuts,
		FieldShortcutHintStyle:   &d.ShortcutHintStyle,
		FieldLanguage:            &d.Language,
		FieldFormattingLocale:    &d.FormattingLocale,
		FieldHourCycle:           &d.HourCycle,
		FieldDateStyle:           &d.DateStyle,
		FieldFirstDayOfWeek:      &d.FirstDayOfWeek,
		FieldTimeZone:            &d.TimeZone,
		FieldDigits:              &d.Digits,
		FieldResolvedLanguage:    &d.ResolvedLanguage,
	}

	for name, dst := range set {
		if v, ok := s[name]; ok {
			*dst = v
		}
	}

	return d
}

// Fields returns the flat names of the fields that a patch can change, in
// the order of the document.
func Fields() []string {
	out := make([]string, 0, len(fieldSpecs))
	for i := range fieldSpecs {
		out = append(out, fieldSpecs[i].name)
	}

	return out
}

// sortedKeys returns the keys of m in order.
func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}

	slices.Sort(keys)

	return keys
}

// reasonList joins values as "a, b or c".
func reasonList(values []string) string {
	if len(values) < 2 { //nolint:mnd // A list of one value has no "or".
		return strings.Join(values, "")
	}

	return strings.Join(values[:len(values)-1], ", ") + " or " + values[len(values)-1]
}
