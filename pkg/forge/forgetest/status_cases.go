package forgetest

import (
	"errors"
	"net/url"
	"slices"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/pkg/forge"
)

// planStatus is a status of the context that the cases use.
func planStatus(state forge.StatusState, description string) forge.Status {
	return forge.Status{
		State:       state,
		Context:     "opendustpan/plan",
		Description: description,
		TargetURL:   "https://dustpan.example/runs/1",
	}
}

// plainHTTP returns an address with the scheme http, which a status refuses.
func plainHTTP() string { return (&url.URL{Scheme: "http", Host: "a.example", Path: "/x"}).String() }

func caseStatusLands(t *testing.T, h Harness) {
	t.Helper()

	ctx := t.Context()
	c := push(t, h, map[string][]byte{"a.txt": []byte("a")})
	want := planStatus(forge.StatusSuccess, "Plan ready: 2 to add")

	if err := h.Forge.SetStatus(ctx, h.Repo, c.ID, want); err != nil {
		t.Fatalf("SetStatus = %v", err)
	}

	got, err := h.Fixture.Statuses(ctx, c.ID)
	if err != nil {
		t.Fatalf("statuses: %v", err)
	}

	if !slices.Equal(got, []forge.Status{want}) {
		t.Fatalf("the forge shows %+v, want %+v", got, want)
	}

	// A status with no description and no address is valid.
	bare := forge.Status{State: forge.StatusPending, Context: "opendustpan/apply", Description: "", TargetURL: ""}
	if err := h.Forge.SetStatus(ctx, h.Repo, c.ID, bare); err != nil {
		t.Fatalf("SetStatus with no description and no address = %v", err)
	}
}

func caseStatusReplaces(t *testing.T, h Harness) {
	t.Helper()

	ctx := t.Context()
	c := push(t, h, map[string][]byte{"a.txt": []byte("a")})
	other := forge.Status{State: forge.StatusFailure, Context: "opendustpan/policy", Description: "", TargetURL: ""}

	statuses := []forge.Status{
		planStatus(forge.StatusPending, "Plan runs"),
		other,
		planStatus(forge.StatusSuccess, "Plan ready"),
	}

	for _, s := range statuses {
		if err := h.Forge.SetStatus(ctx, h.Repo, c.ID, s); err != nil {
			t.Fatalf("SetStatus of %+v = %v", s, err)
		}
	}

	got, err := h.Fixture.Statuses(ctx, c.ID)
	if err != nil {
		t.Fatalf("statuses: %v", err)
	}

	// The second status of the context "opendustpan/plan" replaces the first,
	// and the status of the other context stays.
	want := []forge.Status{planStatus(forge.StatusSuccess, "Plan ready"), other}
	if !slices.Equal(got, want) {
		t.Fatalf("the forge shows %+v, want %+v", got, want)
	}

	// A status belongs to its commit: a newer commit has none.
	next := push(t, h, map[string][]byte{"a.txt": []byte("b")})

	if none, err := h.Fixture.Statuses(ctx, next.ID); err != nil || len(none) != 0 {
		t.Fatalf("the next commit has the statuses %+v (error %v), want none", none, err)
	}
}

func caseStatusMissingCommit(t *testing.T, h Harness) {
	t.Helper()

	push(t, h, map[string][]byte{"a.txt": []byte("a")})

	err := h.Forge.SetStatus(t.Context(), h.Repo, missingCommit(), planStatus(forge.StatusSuccess, "Plan ready"))
	if !errors.Is(err, forge.ErrNotFound) {
		t.Fatalf("SetStatus of a missing commit = %v, want ErrNotFound", err)
	}
}

func caseStatusInvalid(t *testing.T, h Harness) {
	t.Helper()

	ctx := t.Context()
	c := push(t, h, map[string][]byte{"a.txt": []byte("a")})
	before := callCount(ctx, t, h)

	ok := planStatus(forge.StatusSuccess, "Plan ready")
	with := func(change func(*forge.Status)) forge.Status {
		s := ok
		change(&s)

		return s
	}

	tests := []struct {
		name   string
		sha    string
		status forge.Status
		want   error
	}{
		{"an unknown state", c.ID, with(func(s *forge.Status) { s.State = "done" }), forge.ErrInvalid},
		{"no state", c.ID, with(func(s *forge.Status) { s.State = "" }), forge.ErrInvalid},
		{"no context", c.ID, with(func(s *forge.Status) { s.Context = "" }), forge.ErrInvalid},
		{"a context of spaces", c.ID, with(func(s *forge.Status) { s.Context = "   " }), forge.ErrInvalid},
		{
			"a control character in the context",
			c.ID,
			with(func(s *forge.Status) { s.Context = "a\nb" }),
			forge.ErrInvalid,
		},
		{"a context that is not UTF-8", c.ID, with(func(s *forge.Status) { s.Context = "a\xffb" }), forge.ErrInvalid},
		{
			"a context above the limit", c.ID,
			with(func(s *forge.Status) { s.Context = strings.Repeat("c", forge.MaxStatusContextBytes+1) }),
			forge.ErrTooLarge,
		},
		{
			"a line break in the description",
			c.ID,
			with(func(s *forge.Status) { s.Description = "a\nb" }),
			forge.ErrInvalid,
		},
		{
			"a description above the limit", c.ID,
			with(func(s *forge.Status) { s.Description = strings.Repeat("d", forge.MaxStatusDescriptionBytes+1) }),
			forge.ErrTooLarge,
		},
		{
			"an address with the scheme http",
			c.ID,
			with(func(s *forge.Status) { s.TargetURL = plainHTTP() }),
			forge.ErrInvalid,
		},
		{
			"an address with a script scheme",
			c.ID,
			with(func(s *forge.Status) { s.TargetURL = "javascript:alert(1)" }),
			forge.ErrInvalid,
		},
		{
			"an address with user information",
			c.ID,
			with(func(s *forge.Status) { s.TargetURL = "https://u:p@a.example/" }),
			forge.ErrInvalid,
		},
		{
			"an address with a space",
			c.ID,
			with(func(s *forge.Status) { s.TargetURL = "https://a.example/a b" }),
			forge.ErrInvalid,
		},
		{"an address with no host", c.ID, with(func(s *forge.Status) { s.TargetURL = "https:///x" }), forge.ErrInvalid},
		{
			"an address above the limit", c.ID,
			with(func(s *forge.Status) {
				s.TargetURL = "https://a.example/" + strings.Repeat("p", forge.MaxStatusURLBytes)
			}),
			forge.ErrTooLarge,
		},
		{"a commit ID that is a branch name", "main", ok, forge.ErrInvalid},
		{"a short commit ID", c.ID[:10], ok, forge.ErrInvalid},
		{"a commit ID in upper case", strings.ToUpper(c.ID), ok, forge.ErrInvalid},
		{"a commit ID with a path", c.ID + "/../x", ok, forge.ErrInvalid},
	}

	for _, tc := range tests {
		if err := h.Forge.SetStatus(ctx, h.Repo, tc.sha, tc.status); !errors.Is(err, tc.want) {
			t.Errorf("SetStatus with %s = %v, want %v", tc.name, err, tc.want)
		}
	}

	if err := h.Forge.SetStatus(ctx, "no-slash", c.ID, ok); !errors.Is(err, forge.ErrInvalid) {
		t.Errorf("SetStatus of a bad repository path = %v, want ErrInvalid", err)
	}

	if after := callCount(ctx, t, h); after != before {
		t.Fatalf("an invalid status made %d calls to the forge, want none", after-before)
	}

	// Nothing reached the forge.
	if got, err := h.Fixture.Statuses(ctx, c.ID); err != nil || len(got) != 0 {
		t.Fatalf("the forge shows %+v (error %v) after invalid statuses, want none", got, err)
	}
}

func caseStatusBoundaries(t *testing.T, h Harness) {
	t.Helper()

	ctx := t.Context()
	c := push(t, h, map[string][]byte{"a.txt": []byte("a")})

	// The limits themselves are valid.
	longest := forge.Status{
		State:       forge.StatusError,
		Context:     strings.Repeat("c", forge.MaxStatusContextBytes),
		Description: strings.Repeat("d", forge.MaxStatusDescriptionBytes),
		TargetURL:   "https://a.example/" + strings.Repeat("p", forge.MaxStatusURLBytes-len("https://a.example/")),
	}

	if err := h.Forge.SetStatus(ctx, h.Repo, c.ID, longest); err != nil {
		t.Fatalf("SetStatus at the limits = %v", err)
	}

	got, err := h.Fixture.Statuses(ctx, c.ID)
	if err != nil || !slices.Equal(got, []forge.Status{longest}) {
		t.Fatalf("the forge shows %d statuses (error %v), want the one at the limits", len(got), err)
	}
}
