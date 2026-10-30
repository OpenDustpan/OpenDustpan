package profile_test

import (
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/profile"
)

const day = 24 * time.Hour

func t0() time.Time { return time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC) }

func key(id string, st profile.KeyState, created time.Time) profile.Key {
	k := profile.Key{
		CreatedAt: created, ActivatedAt: time.Time{}, RetiredAt: time.Time{}, DeletedAt: time.Time{},
		ID: profile.KeyID(id), Namespace: "acme", State: st, Public: []byte("p"), Sealed: nil,
	}

	if st.HoldsPrivate() {
		k.Sealed = []byte("s")
	}

	if st == profile.KeyActive {
		k.ActivatedAt = created
	}

	return k
}

func retired(id string, created, at time.Time) profile.Key {
	k := key(id, profile.KeyRetired, created)
	k.RetiredAt = at

	return k
}

// keyWith returns a key with a sealed key and a state that the caller sets.
func keyWith(id, ns string, st profile.KeyState, sealed []byte) profile.Key {
	return profile.Key{
		CreatedAt: t0(), ActivatedAt: time.Time{}, RetiredAt: time.Time{}, DeletedAt: time.Time{},
		ID: profile.KeyID(id), Namespace: namespace.Name(ns), State: st, Public: []byte("p"), Sealed: sealed,
	}
}

func mv(at time.Time, id string, from, to profile.KeyState) profile.KeyMove {
	return profile.KeyMove{At: at, ID: profile.KeyID(id), From: from, To: to}
}

func moves(m ...profile.KeyMove) profile.KeyChange {
	return profile.KeyChange{Add: nil, Move: m, Initial: false}
}

func adds(k ...profile.Key) profile.KeyChange {
	return profile.KeyChange{Add: k, Move: nil, Initial: false}
}

// planCase is one case of TestPlanRotationAtEachBoundary.
type planCase struct {
	name     string
	keys     []profile.Key
	at       time.Time
	opts     profile.RotationOptions
	moves    []profile.KeyMove
	newNext  bool
	newAct   bool
	wantErr  error
	promoted bool
}

func plan(name string, keys []profile.Key, at time.Time, kind profile.RotationKind, promote bool) planCase {
	return planCase{
		name: name, keys: keys, at: at, opts: profile.RotationOptions{Kind: kind, Promote: promote}, moves: nil,
		newNext: false, newAct: false, wantErr: nil, promoted: false,
	}
}

func (c planCase) wantMoves(m ...profile.KeyMove) planCase {
	c.moves = m

	return c
}

func (c planCase) makesNext() planCase {
	c.newNext = true

	return c
}

func (c planCase) makesActive() planCase {
	c.newAct = true

	return c
}

func (c planCase) promotes() planCase {
	c.promoted = true

	return c
}

func (c planCase) fails(err error) planCase {
	c.wantErr = err

	return c
}

func planCases() []planCase {
	active := key("oik-active", profile.KeyActive, t0())
	next := key("oik-next", profile.KeyNext, t0())
	both := []profile.Key{active, next}
	old := []profile.Key{active, retired("oik-old", t0().Add(-day), t0())}
	end := t0().Add(90 * day)
	one := t0().Add(day)
	sched, normal := profile.RotateScheduled, profile.RotateNormal
	promote := []profile.KeyMove{
		mv(end, "oik-active", profile.KeyActive, profile.KeyRetired),
		mv(end, "oik-next", profile.KeyNext, profile.KeyActive),
	}

	return []planCase{
		plan("before the end", both, end.Add(-time.Second), sched, true),
		plan("at the end", both, end, sched, true).wantMoves(promote...).promotes(),
		plan("at the end with no copy of the next key", both, end, sched, false),
		plan("at the end with no next key", []profile.Key{active}, end, sched, true).makesNext(),
		plan("83 days: next key made", []profile.Key{active}, t0().Add(83*day), sched, true).makesNext(),
		plan("83 days less a second: none", []profile.Key{active}, t0().Add(83*day-time.Second), sched, true),
		plan("83 days with a next key: none", both, t0().Add(83*day), sched, true),
		plan("retired for 7 days is deleted", old, t0().Add(7*day), sched, true).wantMoves(
			mv(t0().Add(7*day), "oik-old", profile.KeyRetired, profile.KeyDeleted),
		),
		plan("retired for less than 7 days stays", old, t0().Add(7*day-time.Second), sched, true),
		plan("normal rotation", both, one, normal, true).wantMoves(
			mv(one, "oik-active", profile.KeyActive, profile.KeyRetired),
			mv(one, "oik-next", profile.KeyNext, profile.KeyActive),
		).makesNext().promotes(),
		plan("normal rotation without a next key", []profile.Key{active}, one, normal, true).makesNext(),
		plan(
			"normal rotation with no copy of the next key",
			both,
			one,
			normal,
			false,
		).fails(profile.ErrIssuerKeyMissing),
		plan("the third retired key deletes the oldest", []profile.Key{
			active,
			next,
			retired("oik-r1", t0().Add(-3*day), t0().Add(-2*day)),
			retired("oik-r2", t0().Add(-2*day), t0().Add(-day)),
		}, one, normal, true).wantMoves(
			mv(one, "oik-active", profile.KeyActive, profile.KeyRetired),
			mv(one, "oik-next", profile.KeyNext, profile.KeyActive),
			mv(one, "oik-r1", profile.KeyRetired, profile.KeyDeleted),
		).makesNext().promotes(),
		plan("emergency replaces everything", []profile.Key{active, next, retired("oik-r1", t0(), t0())}, one,
			profile.RotateEmergency, false).wantMoves(
			mv(one, "oik-active", profile.KeyActive, profile.KeyDeleted),
			mv(one, "oik-next", profile.KeyNext, profile.KeyDeleted),
			mv(one, "oik-r1", profile.KeyRetired, profile.KeyDeleted),
		).makesActive().makesNext(),
		plan("no key", nil, t0(), sched, true),
		plan("unknown kind", []profile.Key{active}, t0(), "weekly", false).fails(profile.ErrInvalid),
	}
}

func sortMoves(m []profile.KeyMove) {
	slices.SortFunc(m, func(a, b profile.KeyMove) int { return strings.Compare(string(a.ID), string(b.ID)) })
}

func TestPlanRotationAtEachBoundary(t *testing.T) {
	t.Parallel()

	for _, tc := range planCases() {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			checkPlan(t, tc)
		})
	}
}

func checkPlan(t *testing.T, tc planCase) {
	t.Helper()

	got, err := profile.PlanRotation(tc.keys, tc.at, tc.opts)
	if tc.wantErr != nil {
		if !errors.Is(err, tc.wantErr) {
			t.Fatalf("error = %v, want %v", err, tc.wantErr)
		}

		return
	}

	if err != nil {
		t.Fatal(err)
	}

	sortMoves(got.Moves)
	sortMoves(tc.moves)

	if !slices.Equal(got.Moves, tc.moves) || got.NewNext != tc.newNext || got.NewActive != tc.newAct ||
		got.Promoted() != tc.promoted {
		t.Errorf("plan = %+v, want moves %v, newNext %v, newActive %v", got, tc.moves, tc.newNext, tc.newAct)
	}

	// The plan never breaks the rules of a key set.
	if _, applyErr := profile.ApplyKeys("acme", tc.keys, moves(got.Moves...)); applyErr != nil {
		t.Fatalf("ApplyKeys: %v", applyErr)
	}
}

func TestCheckKeySet(t *testing.T) {
	t.Parallel()

	a, n := key("oik-a", profile.KeyActive, t0()), key("oik-n", profile.KeyNext, t0())
	r := func(id string) profile.Key { return retired(id, t0(), t0()) }

	for name, tc := range map[string]struct {
		keys []profile.Key
		ok   bool
	}{
		"active and next":           {[]profile.Key{a, n}, true},
		"two active":                {[]profile.Key{a, key("oik-b", profile.KeyActive, t0())}, false},
		"two next":                  {[]profile.Key{a, n, key("oik-b", profile.KeyNext, t0())}, false},
		"three retired":             {[]profile.Key{a, r("oik-1"), r("oik-2"), r("oik-3")}, false},
		"two retired":               {[]profile.Key{a, r("oik-1"), r("oik-2")}, true},
		"next with no active":       {[]profile.Key{n}, false},
		"active with no sealed key": {[]profile.Key{keyWith("oik-x", "acme", profile.KeyActive, nil)}, false},
		"retired with a sealed key": {[]profile.Key{a, keyWith("oik-x", "acme", profile.KeyRetired, []byte("s"))}, false},
	} {
		if err := profile.CheckKeySet(tc.keys); (err == nil) != tc.ok {
			t.Errorf("%s: error = %v", name, err)
		}
	}
}

func TestApplyKeysChecksEachMove(t *testing.T) {
	t.Parallel()

	a := key("oik-a", profile.KeyActive, t0())

	for name, ch := range map[string]profile.KeyChange{
		"wrong state":        moves(mv(t0(), "oik-a", profile.KeyNext, profile.KeyRetired)),
		"unknown key":        moves(mv(t0(), "oik-zz", profile.KeyActive, profile.KeyRetired)),
		"same state":         moves(mv(t0(), "oik-a", profile.KeyActive, profile.KeyActive)),
		"no time":            moves(mv(time.Time{}, "oik-a", profile.KeyActive, profile.KeyRetired)),
		"from deleted":       moves(mv(t0(), "oik-a", profile.KeyDeleted, profile.KeyActive)),
		"initial with a key": {Add: nil, Move: nil, Initial: true},
		"duplicate ID":       adds(key("oik-a", profile.KeyRetired, t0())),
		"other namespace":    adds(keyWith("oik-b", "globex", profile.KeyNext, []byte("s"))),
		"second active":      adds(key("oik-b", profile.KeyActive, t0())),
	} {
		if _, err := profile.ApplyKeys("acme", []profile.Key{a}, ch); err == nil {
			t.Errorf("%s: no error", name)
		}
	}
}

func TestCheckAudience(t *testing.T) {
	t.Parallel()

	const pub = "https://api.example.com"

	for aud, ok := range map[string]bool{
		"sts.amazonaws.com": true, "api://AzureADTokenExchange": true, "https://api.example.org": true,
		"https://api.example.com.evil.net": true, "https://api.example.comx": true,
		"": false, "https://api.example.com": false, "https://api.example.com/": false,
		"https://api.example.com/x/y": false, "HTTPS://API.EXAMPLE.COM": false, "https://api.example.com?a=b": false,
		"a\nb": false, "a\tb": false, "a\x00": false, strings.Repeat("é", 128): false, strings.Repeat("a", 255): true,
	} {
		if err := profile.CheckAudience(aud, pub); (err == nil) != ok {
			t.Errorf("CheckAudience(%.30q) = %v, want ok %v", aud, err, ok)
		}
	}

	if err := profile.CheckAudience("https://api.example.com", ""); err != nil {
		t.Errorf("with no public URL: %v", err)
	}
}

func TestCheckIssuerURL(t *testing.T) {
	t.Parallel()

	for raw, ok := range map[string]bool{
		"": true, "https://files.example.com": true, "https://files.example.com/a/b": true, "https://files.example.com:8443/a": true,
		"http://files.example.com": false, "https://files.example.com/": false, "ftp://x": false, "https://u@files.example.com": false,
		"https://files.example.com?x": false, "https://files.example.com#": false, "https://": false, "/relative": false,
	} {
		if err := profile.CheckIssuerURL(raw); (err == nil) != ok {
			t.Errorf("CheckIssuerURL(%q) = %v, want ok %v", raw, err, ok)
		}
	}
}

func TestCheckClaimName(t *testing.T) {
	t.Parallel()

	for name, ok := range map[string]bool{
		"access": true, "unit_id": true, "ref_protected": true, "https://aws.amazon.com/tags": true,
		"iss": false, "namespace": false, "namespace_id": false, "purpose": false, "Access": false, "a-b": false, "": false,
	} {
		if err := profile.CheckClaimName(name); (err == nil) != ok {
			t.Errorf("CheckClaimName(%q) = %v, want ok %v", name, err, ok)
		}
	}
}

func TestSubjectTemplateAndFreshStep(t *testing.T) {
	t.Parallel()

	got, err := profile.CanonicalSubjectTemplate([]string{"access", "namespace"})
	if err != nil || !slices.Equal(got, []string{"namespace", "access"}) {
		t.Errorf("canonical = %v, %v", got, err)
	}

	def := profile.DefaultIssuerSettings("acme")
	same := def
	url := def
	url.IssuerURL = "https://x.example.com"
	actor := def
	actor.IncludeActorName = true
	block := def
	block.AllowBlockSubjects = false
	tpl := def
	tpl.SubjectTemplate = []string{"namespace"}

	for name, tc := range map[string]struct {
		old, next profile.IssuerSettings
		want      bool
	}{
		"no change": {def, same, false}, "issuerURL": {def, url, true}, "issuerURL cleared": {url, def, true},
		"actor name on": {def, actor, true}, "actor name off": {actor, def, false},
		"block subjects off": {def, block, false}, "block subjects on": {block, def, true}, "template": {def, tpl, true},
	} {
		if got := profile.NeedsFreshStep(tc.old, tc.next); got != tc.want {
			t.Errorf("%s: NeedsFreshStep = %v, want %v", name, got, tc.want)
		}
	}
}

func TestParseKeyID(t *testing.T) {
	t.Parallel()

	id := profile.NewKeyID()
	if _, err := profile.ParseKeyID(string(id)); err != nil || !strings.HasPrefix(string(id), "oik-") || len(id) != 24 {
		t.Errorf("new ID %q: %v", id, err)
	}

	for _, bad := range []string{"", "oik-", "oik-short", "key-aaaaaaaaaaaaaaaaaaaa", "oik-aaaaaaaaaaaaaaaaaaaaa", "oik-aaaaaaaaaaaaaaaaaa\n\n"} {
		if _, err := profile.ParseKeyID(bad); err == nil {
			t.Errorf("ParseKeyID(%q) passes", bad)
		}
	}
}
