package profile_test

import (
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/profile"
)

var t0 = time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)

const day = 24 * time.Hour

func key(id string, st profile.KeyState, created time.Time) profile.Key {
	k := profile.Key{
		CreatedAt: created, ID: profile.KeyID(id), Namespace: "acme", State: st, Public: []byte("p"),
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

func TestPlanRotationAtEachBoundary(t *testing.T) {
	t.Parallel()

	active := key("oik-active", profile.KeyActive, t0)
	next := key("oik-next", profile.KeyNext, t0)

	for _, tc := range []struct {
		name     string
		keys     []profile.Key
		at       time.Time
		kind     profile.RotationKind
		promote  bool
		moves    []profile.KeyMove
		newNext  bool
		newAct   bool
		wantErr  error
		promoted bool
	}{
		{name: "before the end", keys: []profile.Key{active, next}, at: t0.Add(90*day - time.Second), kind: profile.RotateScheduled, promote: true},
		{
			name: "at the end", keys: []profile.Key{active, next}, at: t0.Add(90 * day), kind: profile.RotateScheduled, promote: true,
			moves: []profile.KeyMove{
				{At: t0.Add(90 * day), ID: "oik-active", From: profile.KeyActive, To: profile.KeyRetired},
				{At: t0.Add(90 * day), ID: "oik-next", From: profile.KeyNext, To: profile.KeyActive},
			}, promoted: true,
		},
		{name: "at the end with no copy of the next key", keys: []profile.Key{active, next}, at: t0.Add(90 * day), kind: profile.RotateScheduled},
		{name: "at the end with no next key", keys: []profile.Key{active}, at: t0.Add(90 * day), kind: profile.RotateScheduled, promote: true, newNext: true},
		{name: "83 days: next key made", keys: []profile.Key{active}, at: t0.Add(83 * day), kind: profile.RotateScheduled, promote: true, newNext: true},
		{name: "83 days less a second: none", keys: []profile.Key{active}, at: t0.Add(83*day - time.Second), kind: profile.RotateScheduled, promote: true},
		{name: "83 days with a next key: none", keys: []profile.Key{active, next}, at: t0.Add(83 * day), kind: profile.RotateScheduled, promote: true},
		{
			name: "retired for 7 days is deleted", keys: []profile.Key{active, retired("oik-old", t0.Add(-day), t0)}, at: t0.Add(7 * day),
			kind: profile.RotateScheduled, promote: true,
			moves: []profile.KeyMove{{At: t0.Add(7 * day), ID: "oik-old", From: profile.KeyRetired, To: profile.KeyDeleted}},
		},
		{
			name: "retired for less than 7 days stays", keys: []profile.Key{active, retired("oik-old", t0.Add(-day), t0)},
			at: t0.Add(7*day - time.Second), kind: profile.RotateScheduled, promote: true,
		},
		{
			name: "normal rotation", keys: []profile.Key{active, next}, at: t0.Add(day), kind: profile.RotateNormal, promote: true,
			moves: []profile.KeyMove{
				{At: t0.Add(day), ID: "oik-active", From: profile.KeyActive, To: profile.KeyRetired},
				{At: t0.Add(day), ID: "oik-next", From: profile.KeyNext, To: profile.KeyActive},
			}, newNext: true, promoted: true,
		},
		{name: "normal rotation without a next key", keys: []profile.Key{active}, at: t0.Add(day), kind: profile.RotateNormal, promote: true, newNext: true},
		{name: "normal rotation with no copy of the next key", keys: []profile.Key{active, next}, at: t0.Add(day), kind: profile.RotateNormal, wantErr: profile.ErrIssuerKeyMissing},
		{
			name: "the third retired key deletes the oldest", at: t0.Add(day), kind: profile.RotateNormal, promote: true,
			keys: []profile.Key{
				active, next, retired("oik-r1", t0.Add(-3*day), t0.Add(-2*day)), retired("oik-r2", t0.Add(-2*day), t0.Add(-day)),
			},
			moves: []profile.KeyMove{
				{At: t0.Add(day), ID: "oik-active", From: profile.KeyActive, To: profile.KeyRetired},
				{At: t0.Add(day), ID: "oik-next", From: profile.KeyNext, To: profile.KeyActive},
				{At: t0.Add(day), ID: "oik-r1", From: profile.KeyRetired, To: profile.KeyDeleted},
			}, newNext: true, promoted: true,
		},
		{
			name: "emergency replaces everything", keys: []profile.Key{active, next, retired("oik-r1", t0, t0)}, at: t0.Add(day),
			kind: profile.RotateEmergency,
			moves: []profile.KeyMove{
				{At: t0.Add(day), ID: "oik-active", From: profile.KeyActive, To: profile.KeyDeleted},
				{At: t0.Add(day), ID: "oik-next", From: profile.KeyNext, To: profile.KeyDeleted},
				{At: t0.Add(day), ID: "oik-r1", From: profile.KeyRetired, To: profile.KeyDeleted},
			}, newAct: true, newNext: true,
		},
		{name: "no key", at: t0, kind: profile.RotateScheduled, promote: true},
		{name: "unknown kind", keys: []profile.Key{active}, at: t0, kind: "weekly", wantErr: profile.ErrInvalid},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			plan, err := profile.PlanRotation(tc.keys, tc.at, tc.kind, tc.promote)
			if tc.wantErr != nil {
				if !errors.Is(err, tc.wantErr) {
					t.Fatalf("error = %v, want %v", err, tc.wantErr)
				}

				return
			}

			if err != nil {
				t.Fatal(err)
			}

			sortMoves := func(m []profile.KeyMove) {
				slices.SortFunc(m, func(a, b profile.KeyMove) int { return strings.Compare(string(a.ID), string(b.ID)) })
			}
			sortMoves(plan.Moves)
			sortMoves(tc.moves)

			if !slices.Equal(plan.Moves, tc.moves) || plan.NewNext != tc.newNext || plan.NewActive != tc.newAct ||
				plan.Promoted() != tc.promoted {
				t.Errorf("plan = %+v, want moves %v, newNext %v, newActive %v", plan, tc.moves, tc.newNext, tc.newAct)
			}

			// The plan never breaks the rules of a key set.
			result, applyErr := profile.ApplyKeys("acme", tc.keys, profile.KeyChange{Move: plan.Moves})
			if applyErr != nil {
				t.Fatalf("ApplyKeys: %v", applyErr)
			}

			_ = result
		})
	}
}

func TestCheckKeySet(t *testing.T) {
	t.Parallel()

	a, n := key("oik-a", profile.KeyActive, t0), key("oik-n", profile.KeyNext, t0)

	for name, tc := range map[string]struct {
		keys []profile.Key
		ok   bool
	}{
		"active and next":           {[]profile.Key{a, n}, true},
		"two active":                {[]profile.Key{a, key("oik-b", profile.KeyActive, t0)}, false},
		"two next":                  {[]profile.Key{a, n, key("oik-b", profile.KeyNext, t0)}, false},
		"three retired":             {[]profile.Key{a, retired("oik-1", t0, t0), retired("oik-2", t0, t0), retired("oik-3", t0, t0)}, false},
		"two retired":               {[]profile.Key{a, retired("oik-1", t0, t0), retired("oik-2", t0, t0)}, true},
		"next with no active":       {[]profile.Key{n}, false},
		"active with no sealed key": {[]profile.Key{{ID: "oik-x", State: profile.KeyActive, Namespace: "acme", Public: []byte("p"), CreatedAt: t0}}, false},
		"retired with a sealed key": {[]profile.Key{a, {ID: "oik-x", State: profile.KeyRetired, Namespace: "acme", Public: []byte("p"), Sealed: []byte("s"), CreatedAt: t0}}, false},
	} {
		if err := profile.CheckKeySet(tc.keys); (err == nil) != tc.ok {
			t.Errorf("%s: error = %v", name, err)
		}
	}
}

func TestApplyKeysChecksEachMove(t *testing.T) {
	t.Parallel()

	a := key("oik-a", profile.KeyActive, t0)

	for name, ch := range map[string]profile.KeyChange{
		"wrong state":        {Move: []profile.KeyMove{{At: t0, ID: "oik-a", From: profile.KeyNext, To: profile.KeyRetired}}},
		"unknown key":        {Move: []profile.KeyMove{{At: t0, ID: "oik-zz", From: profile.KeyActive, To: profile.KeyRetired}}},
		"same state":         {Move: []profile.KeyMove{{At: t0, ID: "oik-a", From: profile.KeyActive, To: profile.KeyActive}}},
		"no time":            {Move: []profile.KeyMove{{ID: "oik-a", From: profile.KeyActive, To: profile.KeyRetired}}},
		"from deleted":       {Move: []profile.KeyMove{{At: t0, ID: "oik-a", From: profile.KeyDeleted, To: profile.KeyActive}}},
		"initial with a key": {Initial: true},
		"duplicate ID":       {Add: []profile.Key{key("oik-a", profile.KeyRetired, t0)}},
		"other namespace":    {Add: []profile.Key{{ID: "oik-b", State: profile.KeyNext, Namespace: "globex", Public: []byte("p"), Sealed: []byte("s"), CreatedAt: t0}}},
		"second active":      {Add: []profile.Key{key("oik-b", profile.KeyActive, t0)}},
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
