// Package preferencestest is the conformance suite for preferences.Store.
// Every built-in store runs it, and a custom store should too:
//
//	func TestPreferencesConformance(t *testing.T) {
//		t.Parallel()
//		preferencestest.Run(t, func(t *testing.T) preferencestest.Store { return newMyStore(t) })
//	}
//
// The suite makes the people through the principal port, because a row of
// preferences belongs to a person and goes away with it.
package preferencestest

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/preferences"
	"opendustpan.org/opendustpan/pkg/principal"
)

// Store is every persistence port the suite exercises.
type Store interface {
	preferences.Store
	principal.Store
	audit.Store
}

// RawWriter is a Store that also writes the stored text of a row without the
// checks of the package, as an older version or a restore could leave it. The
// case for a stored field that fails a check runs only on a Store that is a
// RawWriter.
type RawWriter interface {
	Store
	// WriteRawPreferences writes the row of the person personID with the
	// document and the time updatedAtMs as they are.
	WriteRawPreferences(ctx context.Context, personID, document string, updatedAtMs int64) error
}

// Factory returns a fresh, empty store for one subtest.
type Factory func(t *testing.T) Store

// auditPageSize is the page size that a case reads from the audit log.
const auditPageSize = 200

// base is the time of the first change in a case.
var base = time.Date(2026, time.October, 20, 12, 0, 0, 0, time.UTC) //nolint:gochecknoglobals // A fixed test time.

// Run runs every case of the suite against the stores that newStore returns.
func Run(t *testing.T, newStore Factory) {
	t.Helper()

	cases := []struct {
		name string
		test func(t *testing.T, s Store)
	}{
		{name: "a person that changed nothing reads the defaults", test: testDefaults},
		{name: "an update sets a field and the read shows it", test: testSetAndRead},
		{name: "a merge patch keeps the fields that it leaves out", test: testMergeKeeps},
		{name: "null sets a field back to its default", test: testNullResets},
		{name: "the two color schemes change on their own", test: testSchemes},
		{name: "a patch that changes nothing writes nothing", test: testNoOp},
		{name: "a real change writes one entry with the field names only", test: testEntryNamesOnly},
		{name: "each real change writes its own entry", test: testEntryPerChange},
		{name: "the time of a change is the time of the entry in milliseconds", test: testUpdatedAt},
		{name: "a value that is not allowed is refused and changes nothing", test: testHostileValues},
		{name: "a field that the document does not have is refused", test: testUnknownField},
		{name: "a valid value of each field is accepted", test: testValidValues},
		{name: "an entry that fails validation changes nothing", test: testInvalidEntry},
		{name: "an update of a missing person is refused", test: testMissingPerson},
		{name: "a service principal has no preferences", test: testServiceRefused},
		{name: "a deleted person has no preferences", test: testDeletedPerson},
		{name: "a person never reads or changes the row of another person", test: testIsolation},
		{name: "updates at the same time on different fields all apply", test: testConcurrentFields},
		{name: "updates at the same time on one field leave one value", test: testConcurrentSameField},
		{name: "the audit chain stays valid after changes", test: testChainValid},
		{name: "a stored field that fails a check is dropped and never locks the person out", test: testStoredBadField},
		{name: "a change with a version writes only when the version is the stored one", test: testIfMatch},
		{name: "each change gives a version above the one before it", test: testVersionRises},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			tc.test(t, newStore(t))
		})
	}
}

// Person is a person that a case made.
type person struct {
	id      string
	subject string
}

// at returns the time of the nth change.
func at(n int) time.Time { return base.Add(time.Duration(n) * time.Minute) }

// mustPrincipal creates a principal named name, of the given kind.
func mustPrincipal(t *testing.T, s Store, name string, kind principal.Kind) person {
	t.Helper()

	p := principal.Principal{
		CreatedAt:   at(0),
		UpdatedAt:   at(0),
		DisabledAt:  time.Time{},
		ID:          principal.IDPrefix + name,
		Subject:     name + "@example.com",
		DisplayName: "Name of " + name,
		Kind:        kind,
		Source:      principal.SourceLocal,
		Namespace:   "",
		GroupCount:  0,
		BreakGlass:  false,
	}

	e := entry(p.ID, at(0))
	e.Action = "server:manage-principals"

	if err := s.CreatePrincipal(t.Context(), p, e); err != nil {
		t.Fatalf("create principal %q: %v", name, err)
	}

	return person{id: p.ID, subject: p.Subject}
}

// entry returns a valid entry of a preferences change of the person id.
func entry(id string, when time.Time) audit.Entry {
	return audit.Entry{
		At:            when,
		Namespace:     audit.ServerScope,
		Subject:       "alice@example.com",
		IDPSubject:    "",
		TokenID:       "tok-test",
		Action:        preferences.AuditAction,
		Object:        principal.Ref(id),
		Version:       "",
		ValuePath:     "",
		ValueCount:    nil,
		Changes:       nil,
		SourceIP:      "192.0.2.1",
		UserAgent:     "preferencestest",
		Channel:       audit.ChannelAPI,
		Kind:          audit.KindChange,
		Outcome:       audit.OutcomeSuccess,
		RequestID:     "",
		ObjectID:      "",
		PrevHash:      "",
		Hash:          "",
		Sequence:      0,
		FenceVersion:  0,
		Reason:        "",
		PrincipalKind: audit.PrincipalKindPerson,
		JobID:         "",
	}
}

// update runs Update for p with a patch of text values and fails the case on
// an error.
func update(t *testing.T, s Store, p person, n int, patch preferences.Patch) preferences.Result {
	t.Helper()

	res, err := s.Update(t.Context(), p.id, patch, entry(p.id, at(n)), nil)
	if err != nil {
		t.Fatalf("Update(%v): %v", patch, err)
	}

	return res
}

// get reads the preferences of p and fails the case on an error.
func get(t *testing.T, s Store, p person) preferences.Document {
	t.Helper()

	doc, err := s.Get(t.Context(), p.id)
	if err != nil {
		t.Fatalf("Get(%s): %v", p.id, err)
	}

	return doc
}

// changeEntries returns the preferences entries of the audit log, newest
// first.
func changeEntries(t *testing.T, s Store) []audit.Entry {
	t.Helper()

	listing, err := s.List(t.Context(), audit.ServerScope, audit.Page{Number: 1, Size: auditPageSize})
	if err != nil {
		t.Fatalf("List audit: %v", err)
	}

	var out []audit.Entry

	for i := range listing.Entries {
		if listing.Entries[i].Action == preferences.AuditAction {
			out = append(out, listing.Entries[i])
		}
	}

	return out
}

func set(pairs ...string) preferences.Patch {
	p := preferences.Patch{}

	for i := 0; i+1 < len(pairs); i += 2 {
		p[pairs[i]] = preferences.Text(pairs[i+1])
	}

	return p
}

func reset(names ...string) preferences.Patch {
	p := preferences.Patch{}
	for _, n := range names {
		p[n] = nil
	}

	return p
}

func testDefaults(t *testing.T, s Store) {
	t.Helper()

	p := mustPrincipal(t, s, "alice", principal.KindPerson)

	got := get(t, s, p)
	if got != preferences.Default() {
		t.Errorf("Get = %+v, want the defaults %+v", got, preferences.Default())
	}

	if !got.UpdatedAt.IsZero() {
		t.Errorf("UpdatedAt = %v, want the zero time", got.UpdatedAt)
	}
}

func testSetAndRead(t *testing.T, s Store) {
	t.Helper()

	p := mustPrincipal(t, s, "alice", principal.KindPerson)

	res := update(t, s, p, 1, set(
		preferences.FieldTheme, "terminal",
		preferences.FieldLanguage, "ar",
		preferences.FieldFormattingLocale, "ar-EG",
		preferences.FieldDigits, "latn",
		preferences.FieldTimeZone, "Europe/Berlin",
	))

	want := preferences.Default()
	want.Theme = "terminal"
	want.Language = "ar"
	want.FormattingLocale = "ar-EG"
	want.Digits = "latn"
	want.TimeZone = "Europe/Berlin"
	want.UpdatedAt = at(1)

	if !sameDocument(res.Document, want) {
		t.Errorf("Update document = %+v, want %+v", res.Document, want)
	}

	if got := get(t, s, p); !sameDocument(got, want) {
		t.Errorf("Get = %+v, want %+v", got, want)
	}

	wantChanged := []string{
		preferences.FieldTheme, preferences.FieldLanguage, preferences.FieldFormattingLocale,
		preferences.FieldTimeZone, preferences.FieldDigits,
	}
	if fmt.Sprint(res.Changed) != fmt.Sprint(wantChanged) {
		t.Errorf("Changed = %v, want %v", res.Changed, wantChanged)
	}
}

// withoutTime returns d with no time of change.
func withoutTime(d preferences.Document) preferences.Document {
	d.UpdatedAt = time.Time{}

	return d
}

// sameDocument compares two documents, with the time by instant.
func sameDocument(a, b preferences.Document) bool {
	if !a.UpdatedAt.Equal(b.UpdatedAt) {
		return false
	}

	a.UpdatedAt, b.UpdatedAt = time.Time{}, time.Time{}

	return a == b
}

func testMergeKeeps(t *testing.T, s Store) {
	t.Helper()

	p := mustPrincipal(t, s, "alice", principal.KindPerson)
	update(t, s, p, 1, set(preferences.FieldLanguage, "he", preferences.FieldHourCycle, "h23"))
	update(t, s, p, 2, set(preferences.FieldDateStyle, "iso8601"))

	got := get(t, s, p)
	if got.Language != "he" || got.HourCycle != "h23" || got.DateStyle != "iso8601" {
		t.Errorf("Get = %+v, want language he, hourCycle h23 and dateStyle iso8601", got)
	}
}

func testNullResets(t *testing.T, s Store) {
	t.Helper()

	p := mustPrincipal(t, s, "alice", principal.KindPerson)
	update(t, s, p, 1, set(
		preferences.FieldTheme, "terminal", preferences.FieldLanguage, "uk", preferences.FieldFirstDayOfWeek, "mon",
	))

	res := update(t, s, p, 2, reset(preferences.FieldTheme, preferences.FieldLanguage))
	if fmt.Sprint(res.Changed) != fmt.Sprint([]string{preferences.FieldTheme, preferences.FieldLanguage}) {
		t.Errorf("Changed = %v, want theme and language", res.Changed)
	}

	got := get(t, s, p)
	if got.Theme != "default" || got.Language != "" || got.FirstDayOfWeek != "mon" {
		t.Errorf("Get = %+v, want theme default, no language and firstDayOfWeek mon", got)
	}
}

func testSchemes(t *testing.T, s Store) {
	t.Helper()

	p := mustPrincipal(t, s, "alice", principal.KindPerson)
	update(t, s, p, 1, set(preferences.FieldTerminalSchemeLight, "solarized-light"))
	update(t, s, p, 2, set(preferences.FieldTerminalSchemeDark, "tokyo-night"))

	got := get(t, s, p)
	if got.TerminalSchemes.Light != "solarized-light" || got.TerminalSchemes.Dark != "tokyo-night" {
		t.Errorf("schemes = %+v, want solarized-light and tokyo-night", got.TerminalSchemes)
	}

	update(t, s, p, 3, reset(preferences.FieldTerminalSchemeLight, preferences.FieldTerminalSchemeDark))

	got = get(t, s, p)
	if got.TerminalSchemes != preferences.Default().TerminalSchemes {
		t.Errorf("schemes = %+v, want the defaults", got.TerminalSchemes)
	}
}

func testNoOp(t *testing.T, s Store) {
	t.Helper()

	p := mustPrincipal(t, s, "alice", principal.KindPerson)

	// A person with no row: nothing to change, nothing to write.
	for name, patch := range map[string]preferences.Patch{
		"an empty patch":                      {},
		"null on a field with no value":       reset(preferences.FieldLanguage, preferences.FieldTheme),
		"the default value of a field":        set(preferences.FieldTheme, "default", preferences.FieldDateStyle, "locale"),
		"null on both schemes with no values": reset(preferences.FieldTerminalSchemeLight),
	} {
		res, err := s.Update(t.Context(), p.id, patch, entry(p.id, at(1)), nil)
		if err != nil {
			t.Fatalf("%s: Update: %v", name, err)
		}

		if len(res.Changed) != 0 || !res.Document.UpdatedAt.IsZero() {
			t.Errorf("%s: result = %+v, want no change and no time", name, res)
		}
	}

	if got := get(t, s, p); !got.UpdatedAt.IsZero() {
		t.Errorf("UpdatedAt = %v after no-op patches, want the zero time", got.UpdatedAt)
	}

	// A person with a row: the same value again changes nothing, and the time
	// stays.
	update(t, s, p, 2, set(preferences.FieldLanguage, "ru"))

	res, err := s.Update(t.Context(), p.id, set(preferences.FieldLanguage, "ru"), entry(p.id, at(9)), nil)
	if err != nil {
		t.Fatalf("Update again: %v", err)
	}

	if len(res.Changed) != 0 || !res.Document.UpdatedAt.Equal(at(2)) {
		t.Errorf("result = %+v, want no change and the time %v", res, at(2))
	}

	if got := get(t, s, p); !got.UpdatedAt.Equal(at(2)) {
		t.Errorf("UpdatedAt = %v, want %v", got.UpdatedAt, at(2))
	}

	if n := len(changeEntries(t, s)); n != 1 {
		t.Errorf("preferences entries = %d, want 1: only the real change writes one", n)
	}
}

func testEntryNamesOnly(t *testing.T, s Store) {
	t.Helper()

	p := mustPrincipal(t, s, "alice", principal.KindPerson)

	e := entry(p.id, at(1))
	e.Changes = []audit.Change{{Key: "sessionId", Old: nil, New: "ses-1", Layer: "", TakesEffect: ""}}

	const secretish = "Europe/Berlin"

	patch := set(preferences.FieldTimeZone, secretish, preferences.FieldLanguage, "ar")

	if _, err := s.Update(t.Context(), p.id, patch, e, nil); err != nil {
		t.Fatalf("Update: %v", err)
	}

	entries := changeEntries(t, s)
	if len(entries) != 1 {
		t.Fatalf("preferences entries = %d, want 1", len(entries))
	}

	got := entries[0]
	if got.Object != principal.Ref(p.id) || got.Outcome != audit.OutcomeSuccess || got.Kind != audit.KindChange ||
		got.TokenID != "tok-test" {
		t.Errorf("entry = %+v, want a success change of %s by the token tok-test", got, principal.Ref(p.id))
	}

	checkNameChanges(t, got, preferences.FieldTimeZone, preferences.FieldLanguage)

	text := fmt.Sprintf("%+v", got)
	for _, value := range []string{secretish, `"ar"`} {
		if strings.Contains(text, value) {
			t.Errorf("entry holds the value %s: %s", value, text)
		}
	}
}

// checkNameChanges fails the case unless the entry holds the change of the
// session of the request and one change for each of names: the name, no old
// value, and the word that says it changed.
func checkNameChanges(t *testing.T, got audit.Entry, names ...string) {
	t.Helper()

	keys := map[string]audit.Change{}
	for _, c := range got.Changes {
		keys[c.Key] = c
	}

	if len(keys) != len(names)+1 || keys["sessionId"].New != "ses-1" {
		t.Fatalf("changes = %+v, want the session and the field names %v", got.Changes, names)
	}

	for _, name := range names {
		c, ok := keys[name]
		if !ok || c.Old != nil || c.New != "changed" {
			t.Errorf("change %q = %+v, %v; want the name with no old value and the word changed", name, c, ok)
		}
	}
}

func testEntryPerChange(t *testing.T, s Store) {
	t.Helper()

	p := mustPrincipal(t, s, "alice", principal.KindPerson)
	update(t, s, p, 1, set(preferences.FieldTheme, "terminal"))
	update(t, s, p, 2, set(preferences.FieldColorMode, "dark"))
	update(t, s, p, 3, reset(preferences.FieldTheme))

	if n := len(changeEntries(t, s)); n != 3 {
		t.Errorf("preferences entries = %d, want 3", n)
	}
}

func testUpdatedAt(t *testing.T, s Store) {
	t.Helper()

	p := mustPrincipal(t, s, "alice", principal.KindPerson)
	when := base.Add(1234567 * time.Microsecond)

	res, err := s.Update(t.Context(), p.id, set(preferences.FieldTheme, "terminal"), entry(p.id, when), nil)
	if err != nil {
		t.Fatalf("Update: %v", err)
	}

	want := when.Truncate(time.Millisecond)
	if !res.Document.UpdatedAt.Equal(want) {
		t.Errorf("UpdatedAt = %v, want %v: the time in milliseconds", res.Document.UpdatedAt, want)
	}

	if got := get(t, s, p); !got.UpdatedAt.Equal(want) {
		t.Errorf("Get UpdatedAt = %v, want %v", got.UpdatedAt, want)
	}

	if loc := get(t, s, p).UpdatedAt.Location(); loc != time.UTC {
		t.Errorf("UpdatedAt location = %v, want UTC", loc)
	}
}

// hostile holds, for each field, values that no layer may accept.
func hostile() map[string][]string {
	long := strings.Repeat("a", 70)
	controls := []string{
		"",
		" ",
		"en\n",
		"en\r\nX: y",
		"en\x00",
		string(rune(0x202e)) + "en",
		"en;DROP TABLE x",
		"'; --",
		"<script>",
		"en us",
		"en_US",
	}

	return map[string][]string{
		preferences.FieldTheme:               append([]string{"Default", "dark", long}, controls...),
		preferences.FieldColorMode:           append([]string{"auto", "SYSTEM"}, controls...),
		preferences.FieldTerminalSchemeLight: append([]string{"Solarized", "a_b", "a/b", "../x", long}, controls...),
		preferences.FieldTerminalSchemeDark:  append([]string{"é", "a.b", strings.Repeat("a", 65)}, controls...),
		preferences.FieldShortcutHints:       append([]string{"true", "ON"}, controls...),
		preferences.FieldSingleKeyShortcuts:  append([]string{"yes"}, controls...),
		preferences.FieldShortcutHintStyle:   append([]string{"keycaps"}, controls...),
		preferences.FieldLanguage: append(
			[]string{"e", long, "en-", "-en", "ar_EG", "日本語", "en-١"},
			controls...,
		),
		preferences.FieldFormattingLocale: append(
			[]string{"ar-u-nu-latn", "en-x-private", "x-foo", "en-US-u-hc-h23", "e"}, controls...,
		),
		preferences.FieldHourCycle:        append([]string{"h11", "h24", "H12"}, controls...),
		preferences.FieldDateStyle:        append([]string{"ymd", "ISO8601"}, controls...),
		preferences.FieldFirstDayOfWeek:   append([]string{"monday", "Mon", "0"}, controls...),
		preferences.FieldDigits:           append([]string{"LATN", "arabext", "deva", "null"}, controls...),
		preferences.FieldResolvedLanguage: append([]string{"en_US", long}, controls...),
		preferences.FieldTimeZone: append([]string{
			"../etc/passwd", "/etc/localtime", "Europe/../UTC", "Europe//Berlin", "Local", "local", "posixrules",
			"posix/Europe/Berlin", "Europe/Berlin\x00", "Not/AZone", long, "zone.tab", "Europe/Berlin/", "UTC ",
		}, controls...),
	}
}

func testHostileValues(t *testing.T, s Store) {
	t.Helper()

	p := mustPrincipal(t, s, "alice", principal.KindPerson)
	update(t, s, p, 1, set(preferences.FieldTheme, "terminal"))

	before := get(t, s, p)
	entriesBefore := len(changeEntries(t, s))

	for field, values := range hostile() {
		for _, value := range values {
			_, err := s.Update(t.Context(), p.id, set(field, value), entry(p.id, at(5)), nil)

			var fieldErr *preferences.FieldError

			if !errors.Is(err, preferences.ErrInvalidValue) || !errors.As(err, &fieldErr) || fieldErr.Field != field {
				t.Errorf("Update(%s = %q) error = %v, want a field error for %s", field, value, err, field)
			}

			if err != nil && value != "null" && strings.Contains(err.Error(), value) && len(value) > 3 {
				t.Errorf("Update(%s = %q) error %q holds the value", field, value, err)
			}
		}
	}

	if got := get(t, s, p); !sameDocument(got, before) {
		t.Errorf("document = %+v after refused patches, want %+v", got, before)
	}

	if n := len(changeEntries(t, s)); n != entriesBefore {
		t.Errorf("preferences entries = %d, want %d: a refused patch writes none", n, entriesBefore)
	}
}

func testUnknownField(t *testing.T, s Store) {
	t.Helper()

	p := mustPrincipal(t, s, "alice", principal.KindPerson)

	for _, name := range []string{
		"unknown", preferences.FieldUpdatedAt, preferences.FieldTerminalSchemes, "", "Theme", "__proto__", "theme ",
	} {
		_, err := s.Update(t.Context(), p.id, set(name, "x"), entry(p.id, at(1)), nil)
		if !errors.Is(err, preferences.ErrUnknownField) {
			t.Errorf("Update(%q) error = %v, want ErrUnknownField", name, err)
		}

		if err != nil && name != "" && strings.Contains(err.Error(), name) {
			t.Errorf("Update(%q) error %q holds the name", name, err)
		}
	}

	if got := get(t, s, p); got != preferences.Default() {
		t.Errorf("document = %+v, want the defaults", got)
	}
}

func testValidValues(t *testing.T, s Store) {
	t.Helper()

	p := mustPrincipal(t, s, "alice", principal.KindPerson)

	cases := []preferences.Patch{
		set(preferences.FieldTheme, "terminal", preferences.FieldColorMode, "light"),
		set(
			preferences.FieldTerminalSchemeLight,
			"a",
			preferences.FieldTerminalSchemeDark,
			strings.Repeat("z9-", 21)+"z",
		),
		set(preferences.FieldShortcutHints, "off", preferences.FieldSingleKeyShortcuts, "on",
			preferences.FieldShortcutHintStyle, "inverse"),
		set(preferences.FieldLanguage, "zh-Hant-TW", preferences.FieldResolvedLanguage, "af"),
		set(preferences.FieldLanguage, "xx"),
		set(preferences.FieldFormattingLocale, "ar-SA"),
		set(preferences.FieldFormattingLocale, "en-US"),
		set(
			preferences.FieldHourCycle,
			"h12",
			preferences.FieldDateStyle,
			"mdy",
			preferences.FieldFirstDayOfWeek,
			"sun",
		),
		set(preferences.FieldTimeZone, "UTC"),
		set(preferences.FieldTimeZone, "Etc/GMT+5"),
		set(preferences.FieldTimeZone, "America/Argentina/Buenos_Aires"),
		set(preferences.FieldDigits, "arab"),
	}

	for i, patch := range cases {
		if _, err := s.Update(t.Context(), p.id, patch, entry(p.id, at(i+1)), nil); err != nil {
			t.Errorf("Update(%v): %v", patch, err)
		}
	}
}

func testInvalidEntry(t *testing.T, s Store) {
	t.Helper()

	p := mustPrincipal(t, s, "alice", principal.KindPerson)

	for name, mutate := range map[string]func(e *audit.Entry){
		"no subject":      func(e *audit.Entry) { e.Subject = "" },
		"a namespace":     func(e *audit.Entry) { e.Namespace = "acme" },
		"no time":         func(e *audit.Entry) { e.At = time.Time{} },
		"an unknown kind": func(e *audit.Entry) { e.Kind = "bogus" },
		"no object":       func(e *audit.Entry) { e.Object = "" },
		"a hidden reason": func(e *audit.Entry) { e.Reason = "a\nb" },
	} {
		for _, patch := range []preferences.Patch{set(preferences.FieldTheme, "terminal"), {}} {
			e := entry(p.id, at(1))
			mutate(&e)

			if _, err := s.Update(t.Context(), p.id, patch, e, nil); !errors.Is(err, audit.ErrInvalidEntry) {
				t.Errorf("%s, patch %v: error = %v, want ErrInvalidEntry", name, patch, err)
			}
		}
	}

	if got := get(t, s, p); got != preferences.Default() {
		t.Errorf("document = %+v, want the defaults: an invalid entry changes nothing", got)
	}
}

func testMissingPerson(t *testing.T, s Store) {
	t.Helper()

	for _, id := range []string{principal.IDPrefix + "nobody", "", "not-an-id", "prn-" + strings.Repeat("a", 200)} {
		if _, err := s.Get(t.Context(), id); !errors.Is(err, preferences.ErrPersonNotFound) {
			t.Errorf("Get(%q) error = %v, want ErrPersonNotFound", id, err)
		}

		_, err := s.Update(
			t.Context(),
			id,
			set(preferences.FieldTheme, "terminal"),
			entry(principal.IDPrefix+"x", at(1)),
			nil,
		)
		if !errors.Is(err, preferences.ErrPersonNotFound) {
			t.Errorf("Update(%q) error = %v, want ErrPersonNotFound", id, err)
		}
	}

	if n := len(changeEntries(t, s)); n != 0 {
		t.Errorf("preferences entries = %d, want 0", n)
	}
}

func testServiceRefused(t *testing.T, s Store) {
	t.Helper()

	svc := mustPrincipal(t, s, "robot", principal.KindService)

	if _, err := s.Get(t.Context(), svc.id); !errors.Is(err, preferences.ErrPersonNotFound) {
		t.Errorf("Get error = %v, want ErrPersonNotFound", err)
	}

	_, err := s.Update(t.Context(), svc.id, set(preferences.FieldTheme, "terminal"), entry(svc.id, at(1)), nil)
	if !errors.Is(err, preferences.ErrPersonNotFound) {
		t.Errorf("Update error = %v, want ErrPersonNotFound", err)
	}
}

func testDeletedPerson(t *testing.T, s Store) {
	t.Helper()

	p := mustPrincipal(t, s, "alice", principal.KindPerson)
	update(t, s, p, 1, set(preferences.FieldTheme, "terminal"))

	e := entry(p.id, at(2))
	e.Action = "server:manage-principals"

	if err := s.DeletePrincipal(t.Context(), p.id, e); err != nil {
		t.Fatalf("DeletePrincipal: %v", err)
	}

	if _, err := s.Get(t.Context(), p.id); !errors.Is(err, preferences.ErrPersonNotFound) {
		t.Errorf("Get error = %v, want ErrPersonNotFound", err)
	}

	_, err := s.Update(t.Context(), p.id, set(preferences.FieldTheme, "default"), entry(p.id, at(3)), nil)
	if !errors.Is(err, preferences.ErrPersonNotFound) {
		t.Errorf("Update error = %v, want ErrPersonNotFound", err)
	}

	// A new person with the same subject is a new person with the defaults.
	again := principal.Principal{
		CreatedAt: at(4), UpdatedAt: at(4), DisabledAt: time.Time{}, ID: principal.IDPrefix + "alice2",
		Subject: p.subject, DisplayName: "Alice again", Kind: principal.KindPerson, Source: principal.SourceLocal,
		Namespace: "", GroupCount: 0, BreakGlass: false,
	}

	ce := entry(again.ID, at(4))
	ce.Action = "server:manage-principals"

	if err := s.CreatePrincipal(t.Context(), again, ce); err != nil {
		t.Fatalf("CreatePrincipal again: %v", err)
	}

	if got := get(t, s, person{id: again.ID, subject: again.Subject}); got != preferences.Default() {
		t.Errorf("document of the new person = %+v, want the defaults", got)
	}
}

func testIsolation(t *testing.T, s Store) {
	t.Helper()

	alice := mustPrincipal(t, s, "alice", principal.KindPerson)
	bob := mustPrincipal(t, s, "bob", principal.KindPerson)

	update(t, s, alice, 1, set(preferences.FieldLanguage, "ru", preferences.FieldTheme, "terminal"))

	if got := get(t, s, bob); got != preferences.Default() {
		t.Errorf("document of bob = %+v, want the defaults", got)
	}

	update(t, s, bob, 2, set(preferences.FieldLanguage, "uk"))

	if got := get(t, s, alice); got.Language != "ru" || got.Theme != "terminal" {
		t.Errorf("document of alice = %+v, want language ru and theme terminal", got)
	}

	if got := get(t, s, bob); got.Language != "uk" || got.Theme != "default" {
		t.Errorf("document of bob = %+v, want language uk and theme default", got)
	}
}

func testConcurrentFields(t *testing.T, s Store) {
	t.Helper()

	p := mustPrincipal(t, s, "alice", principal.KindPerson)

	patches := []preferences.Patch{
		set(preferences.FieldTheme, "terminal"),
		set(preferences.FieldColorMode, "dark"),
		set(preferences.FieldLanguage, "ar"),
		set(preferences.FieldHourCycle, "h23"),
		set(preferences.FieldDateStyle, "dmy"),
		set(preferences.FieldFirstDayOfWeek, "sat"),
		set(preferences.FieldTimeZone, "Asia/Dubai"),
		set(preferences.FieldDigits, "arab"),
	}

	var wg sync.WaitGroup

	errs := make(chan error, len(patches))

	for i, patch := range patches {
		wg.Go(func() {
			_, err := s.Update(t.Context(), p.id, patch, entry(p.id, at(i+1)), nil)
			errs <- err
		})
	}

	wg.Wait()
	close(errs)

	for err := range errs {
		if err != nil {
			t.Errorf("concurrent Update: %v", err)
		}
	}

	want := preferences.Default()
	want.Theme, want.ColorMode, want.Language, want.HourCycle = "terminal", "dark", "ar", "h23"
	want.DateStyle, want.FirstDayOfWeek, want.TimeZone, want.Digits = "dmy", "sat", "Asia/Dubai", "arab"

	got := get(t, s, p)
	if !sameDocument(withoutTime(got), want) {
		t.Errorf("document = %+v, want every concurrent change", got)
	}

	if n := len(changeEntries(t, s)); n != len(patches) {
		t.Errorf("preferences entries = %d, want %d", n, len(patches))
	}
}

func testConcurrentSameField(t *testing.T, s Store) {
	t.Helper()

	p := mustPrincipal(t, s, "alice", principal.KindPerson)
	values := []string{"en", "fr", "de", "ru", "uk", "he"}

	var wg sync.WaitGroup

	for i, v := range values {
		wg.Go(func() {
			_, err := s.Update(t.Context(), p.id, set(preferences.FieldLanguage, v), entry(p.id, at(i+1)), nil)
			if err != nil {
				t.Errorf("concurrent Update(%s): %v", v, err)
			}
		})
	}

	wg.Wait()

	got := get(t, s, p).Language

	found := false

	for _, v := range values {
		found = found || got == v
	}

	if !found {
		t.Errorf("language = %q, want one of %v", got, values)
	}

	if n := len(changeEntries(t, s)); n != len(values) {
		t.Errorf("preferences entries = %d, want %d: each value differs from the one before", n, len(values))
	}
}

func testChainValid(t *testing.T, s Store) {
	t.Helper()

	p := mustPrincipal(t, s, "alice", principal.KindPerson)
	update(t, s, p, 1, set(preferences.FieldTheme, "terminal"))
	update(t, s, p, 2, set(preferences.FieldLanguage, "he"))

	res, err := s.Check(t.Context(), audit.ServerScope)
	if err != nil {
		t.Fatalf("Check: %v", err)
	}

	if !res.Valid {
		t.Errorf("audit chain check = %+v, want a valid chain", res)
	}
}

// A row with a value that fails a check: the read serves the document with no
// such field, and a patch that resets the field, or changes another field,
// works.
func testStoredBadField(t *testing.T, s Store) {
	t.Helper()

	w, ok := s.(RawWriter)
	if !ok {
		t.Skip("the store writes no raw row")
	}

	p := mustPrincipal(t, s, "alice", principal.KindPerson)

	row := `{"timeZone":"Mars/Olympus"}`
	if err := w.WriteRawPreferences(t.Context(), p.id, row, at(1).UnixMilli()); err != nil {
		t.Fatalf("write the row: %v", err)
	}

	got := get(t, s, p)
	if got.TimeZone != "" || got.Theme != "default" || !got.UpdatedAt.Equal(at(1)) {
		t.Errorf("Get = %+v, want the document with no timeZone and the time of the row", got)
	}

	if res := update(t, s, p, 2, reset(preferences.FieldTimeZone)); res.Document.TimeZone != "" {
		t.Errorf("reset of timeZone: document = %+v, want no timeZone", res.Document)
	}

	res := update(t, s, p, 3, set(preferences.FieldTheme, "terminal"))
	if !slices.Equal(res.Changed, []string{preferences.FieldTheme}) {
		t.Errorf("change of theme: changed = %v, want theme", res.Changed)
	}

	if got := get(t, s, p); got.Theme != "terminal" || got.TimeZone != "" {
		t.Errorf("Get = %+v, want theme terminal and no timeZone", got)
	}

	// A row that is not a document at all gives the defaults.
	err := w.WriteRawPreferences(t.Context(), p.id, `{"theme":"terminal","theme":"default"}`, at(4).UnixMilli())
	if err != nil {
		t.Fatalf("write the row: %v", err)
	}

	if got := get(t, s, p); got.Theme != "default" {
		t.Errorf("Get of a row with a repeated key = %+v, want the defaults", got)
	}

	update(t, s, p, 5, set(preferences.FieldLanguage, "ar"))
}

// A change with ifMatch writes only when ifMatch is the version that the store
// holds. A refused change writes no row and no audit entry.
func testIfMatch(t *testing.T, s Store) {
	t.Helper()

	p := mustPrincipal(t, s, "alice", principal.KindPerson)
	zero := int64(0)

	// No row: version 0.
	res, err := s.Update(t.Context(), p.id, set(preferences.FieldTheme, "terminal"), entry(p.id, at(1)), &zero)
	if err != nil {
		t.Fatalf("Update with version 0 on no row: %v", err)
	}

	version := res.Document.Version()
	if version != at(1).UnixMilli() {
		t.Fatalf("version = %d, want %d", version, at(1).UnixMilli())
	}

	entries := len(changeEntries(t, s))
	stale := version - 1

	for _, ifMatch := range []int64{0, stale, version + 1} {
		_, refused := s.Update(t.Context(), p.id, set(preferences.FieldTheme, "default"), entry(p.id, at(2)), &ifMatch)
		if !errors.Is(refused, preferences.ErrPreconditionFailed) {
			t.Errorf("Update with version %d: error = %v, want ErrPreconditionFailed", ifMatch, refused)
		}
	}

	// A patch that changes nothing is refused too, when the version is old.
	_, err = s.Update(t.Context(), p.id, preferences.Patch{}, entry(p.id, at(2)), &stale)
	if !errors.Is(err, preferences.ErrPreconditionFailed) {
		t.Errorf("no-op with an old version: error = %v, want ErrPreconditionFailed", err)
	}

	if got := get(t, s, p); got.Theme != "terminal" || got.Version() != version {
		t.Errorf("Get = %+v, want the document before the refused changes", got)
	}

	if n := len(changeEntries(t, s)); n != entries {
		t.Errorf("entries = %d, want %d: a refused change writes none", n, entries)
	}

	_, err = s.Update(t.Context(), p.id, set(preferences.FieldTheme, "default"), entry(p.id, at(3)), &version)
	if err != nil {
		t.Errorf("Update with the stored version: %v", err)
	}
}

// Each change gives a version above the one before it, also when the entry
// has the same time or an older time.
func testVersionRises(t *testing.T, s Store) {
	t.Helper()

	p := mustPrincipal(t, s, "alice", principal.KindPerson)
	last := int64(0)

	for i, when := range []time.Time{at(5), at(5), at(1)} {
		theme := []string{"terminal", "default", "terminal"}[i]

		res, err := s.Update(t.Context(), p.id, set(preferences.FieldTheme, theme), entry(p.id, when), nil)
		if err != nil {
			t.Fatalf("Update %d: %v", i, err)
		}

		if v := res.Document.Version(); v <= last {
			t.Errorf("version after change %d = %d, want above %d", i, v, last)
		}

		last = res.Document.Version()
	}
}
