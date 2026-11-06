package github_test

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/e2e/githubfake"
	"opendustpan.org/opendustpan/pkg/forge"
)

const (
	cannedLogin    = "dustpan-user"
	listCommentsAt = "GET /repos/acme/infra/issues/7/comments?per_page=100&page="
	userPath       = "GET /user"
	postCommentAt  = "POST /repos/acme/infra/issues/7/comments"
)

// userAnswer is the answer to the call that names the user of the token.
func userAnswer() hostAnswer {
	return okAnswer(map[string]any{"id": 5, "login": cannedLogin})
}

// commentJSON returns the answer for a comment of the pull request 7.
func commentJSON(id int, login string, authorID int, body string) map[string]any {
	return map[string]any{
		"id": id, "body": body,
		"html_url":  fmt.Sprintf("https://github.example/acme/infra/pull/7#issuecomment-%d", id),
		"issue_url": "https://api.github.example/repos/acme/infra/issues/7",
		"user":      map[string]any{"id": authorID, "login": login},
	}
}

const markerPlan = "<!-- opendustpan:plan -->"

func planRequest() forge.CommentRequest {
	return forge.CommentRequest{Marker: "plan", Body: "Plan ready"}
}

// TestUpsertCommentEditsOnlyItsOwn checks which comment of the list UpsertComment
// edits: the marker must be the first line, and the author must be the account
// of the connection.
func TestUpsertCommentEditsOnlyItsOwn(t *testing.T) {
	t.Parallel()

	person := commentJSON(11, "other-dev", 9, markerPlan+"\nforged")
	noMarker := commentJSON(12, cannedLogin, 5, "Plan ready")
	late := commentJSON(13, cannedLogin, 5, "Text\n"+markerPlan)
	otherKey := commentJSON(14, cannedLogin, 5, "<!-- opendustpan:plan2 -->\nText")
	mine := commentJSON(15, cannedLogin, 5, markerPlan+"\r\nPlan one")
	newer := commentJSON(16, cannedLogin, 5, markerPlan+"\nPlan two")
	created := commentJSON(20, cannedLogin, 5, markerPlan+"\nPlan ready")

	tests := []struct {
		name  string
		list  []any
		patch string
	}{
		{"a comment of a person with the marker", []any{person}, ""},
		{"a comment of the connection without the marker", []any{noMarker}, ""},
		{"a marker in a later line", []any{late}, ""},
		{"a marker of another key", []any{otherKey}, ""},
		{"all of them and the own comment", []any{person, noMarker, late, otherKey, mine, newer}, "15"},
		{"the first own comment of the list", []any{newer, mine}, "16"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			checkUpsertChoice(t, tt.list, tt.patch, created)
		})
	}
}

// checkUpsertChoice runs UpsertComment on a page of comments and checks that it
// edits the comment patch, or makes a comment when patch is empty.
func checkUpsertChoice(t *testing.T, list []any, patch string, created map[string]any) {
	t.Helper()

	answers := map[string]hostAnswer{
		userPath:             userAnswer(),
		listCommentsAt + "1": okAnswer(list),
		postCommentAt:        {status: http.StatusCreated, body: toJSON(created), header: nil, next: nil},
	}

	if patch != "" {
		answers["PATCH /repos/acme/infra/issues/comments/"+patch] = okAnswer(
			commentJSON(mustInt(patch), cannedLogin, 5, markerPlan+"\nPlan ready"),
		)
	}

	f := cannedForge(t, answers)

	got, err := f.UpsertComment(t.Context(), "acme/infra", "7", planRequest())
	if err != nil {
		t.Fatalf("UpsertComment = %v", err)
	}

	patched := slices.ContainsFunc(f.calls(), func(c string) bool { return strings.HasPrefix(c, "PATCH ") })
	if patched != (patch != "") || f.sent(http.MethodPost) == (patch != "") {
		t.Fatalf("UpsertComment made the calls %v, want an edit = %v", f.calls(), patch != "")
	}

	want := patch
	if patch == "" {
		want = "20"
	}

	if got.ID != want {
		t.Fatalf("UpsertComment answered the comment %s, want %s", got.ID, want)
	}
}

func mustInt(s string) int {
	n := 0
	for _, r := range s {
		n = n*10 + int(r-'0')
	}

	return n
}

// TestUpsertCommentPages checks that UpsertComment reads the next page after a
// full page, and that it refuses a review request with more comments than it
// reads, because a search that stops early would make a second comment.
func TestUpsertCommentPages(t *testing.T) {
	t.Parallel()

	fill := func(from int) []any {
		items := make([]any, 0, 100)
		for i := range 100 {
			items = append(items, commentJSON(from+i, "other-dev", 9, "Comment"))
		}

		return items
	}

	t.Run("the own comment on the second page", func(t *testing.T) {
		t.Parallel()

		f := cannedForge(t, map[string]hostAnswer{
			userPath:             userAnswer(),
			listCommentsAt + "1": okAnswer(fill(100)),
			listCommentsAt + "2": okAnswer([]any{commentJSON(300, cannedLogin, 5, markerPlan+"\nOld")}),
			"PATCH /repos/acme/infra/issues/comments/300": okAnswer(
				commentJSON(300, cannedLogin, 5, markerPlan+"\nPlan ready"),
			),
		})

		if got, err := f.UpsertComment(t.Context(), "acme/infra", "7", planRequest()); err != nil || got.ID != "300" {
			t.Fatalf("UpsertComment = %+v, %v; want an edit of the comment 300", got, err)
		}
	})

	t.Run("more comments than the most", func(t *testing.T) {
		t.Parallel()

		answers := map[string]hostAnswer{userPath: userAnswer()}
		for p := 1; p <= 10; p++ {
			answers[fmt.Sprintf("%s%d", listCommentsAt, p)] = okAnswer(fill(p * 1000))
		}

		f := cannedForge(t, answers)

		_, err := f.UpsertComment(
			t.Context(),
			"acme/infra",
			"7",
			planRequest(),
		)

		if !errors.Is(
			err,
			forge.ErrTooLarge,
		) ||
			f.sent(http.MethodPost) {
			t.Fatalf("UpsertComment = %v with the calls %v, want ErrTooLarge and no comment", err, f.calls())
		}
	})
}

// TestUpsertCommentRefusesAnAnswer checks that an answer that is not the
// comment of the request is ErrUnavailable and never a comment.
func TestUpsertCommentRefusesAnAnswer(t *testing.T) {
	t.Parallel()

	created := func(c map[string]any) hostAnswer {
		return hostAnswer{status: http.StatusCreated, body: toJSON(c), header: nil, next: nil}
	}
	withURL := func(address string) map[string]any {
		c := commentJSON(20, cannedLogin, 5, markerPlan+"\nPlan ready")
		c["html_url"] = address

		return c
	}
	withID := func(id any) map[string]any {
		c := commentJSON(20, cannedLogin, 5, markerPlan+"\nPlan ready")
		c["id"] = id

		return c
	}

	tests := []struct {
		name   string
		answer hostAnswer
	}{
		{"a comment of another author", created(commentJSON(20, "other-dev", 9, markerPlan+"\nPlan ready"))},
		{"a comment without the marker", created(commentJSON(20, cannedLogin, 5, "Plan ready"))},
		{"an address that is not https", created(withURL("http://github.example/x"))},
		{"no address", created(withURL(""))},
		{"an ID that is text", created(withID("20"))},
		{"an ID of zero", created(withID(0))},
		{"an ID with a fraction", created(withID(20.5))},
		{"an answer that does not parse", hostAnswer{status: http.StatusCreated, body: "{", header: nil, next: nil}},
		{
			"no user",
			created(map[string]any{"id": 20, "body": markerPlan + "\nx", "html_url": "https://github.example/x"}),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			f := cannedForge(t, map[string]hostAnswer{
				userPath: userAnswer(), listCommentsAt + "1": okAnswer([]any{}), postCommentAt: tt.answer,
			})

			got, err := f.UpsertComment(t.Context(), "acme/infra", "7", planRequest())
			if !errors.Is(err, forge.ErrUnavailable) || got.ID != "" {
				t.Fatalf("UpsertComment = %+v, %v; want ErrUnavailable and no comment", got, err)
			}
		})
	}

	t.Run("an own comment with a field that fails the checks", func(t *testing.T) {
		t.Parallel()

		broken := commentJSON(15, cannedLogin, 5, markerPlan+"\nOld")
		broken["id"] = "x"

		f := cannedForge(t, map[string]hostAnswer{
			userPath: userAnswer(), listCommentsAt + "1": okAnswer([]any{broken}),
		})

		_, err := f.UpsertComment(t.Context(), "acme/infra", "7", planRequest())
		if !errors.Is(err, forge.ErrUnavailable) || f.sent(http.MethodPost) || f.sent(http.MethodPatch) {
			t.Fatalf("UpsertComment = %v with the calls %v, want ErrUnavailable and no write", err, f.calls())
		}
	})

	t.Run("an edit that answers another comment", func(t *testing.T) {
		t.Parallel()

		f := cannedForge(t, map[string]hostAnswer{
			userPath:             userAnswer(),
			listCommentsAt + "1": okAnswer([]any{commentJSON(15, cannedLogin, 5, markerPlan+"\nOld")}),
			"PATCH /repos/acme/infra/issues/comments/15": okAnswer(commentJSON(16, cannedLogin, 5, markerPlan+"\nNew")),
		})

		_, err := f.UpsertComment(
			t.Context(),
			"acme/infra",
			"7",
			planRequest(),
		)

		if !errors.Is(
			err,
			forge.ErrUnavailable,
		) {
			t.Fatalf("UpsertComment = %v, want ErrUnavailable", err)
		}
	})
}

// TestCommentCallsMapTheStatus checks the sentinel of each failure of the
// comment calls, and that no error holds the token.
func TestCommentCallsMapTheStatus(t *testing.T) {
	t.Parallel()

	limited := hostAnswer{
		status: http.StatusForbidden,
		body:   "{}",
		header: map[string]string{"Retry-After": "9"},
		next:   nil,
	}

	tests := []struct {
		name   string
		answer hostAnswer
		want   error
	}{
		{"a missing review request", statusAnswer(http.StatusNotFound, `{"message":"Not Found"}`), forge.ErrNotFound},
		{
			"issues that are turned off",
			statusAnswer(http.StatusGone, `{"message":"Issues are disabled"}`),
			forge.ErrRefused,
		},
		{
			"a locked conversation",
			statusAnswer(http.StatusForbidden, `{"message":"Unable to create comment"}`),
			forge.ErrRefused,
		},
		{
			"a text that GitHub refuses",
			statusAnswer(http.StatusUnprocessableEntity, `{"message":"Validation Failed"}`),
			forge.ErrInvalid,
		},
		{"a rate limit", limited, forge.ErrUnavailable},
		{"a bad gateway", statusAnswer(http.StatusBadGateway, ``), forge.ErrUnavailable},
		{"a service that is not available", statusAnswer(http.StatusServiceUnavailable, ``), forge.ErrUnavailable},
		{"a credential that is refused", statusAnswer(http.StatusUnauthorized, `{}`), forge.ErrAuth},
	}

	calls := map[string]func(f *canned) error{
		"UpsertComment": func(f *canned) error {
			_, err := f.UpsertComment(context.Background(), "acme/infra", "7", planRequest())

			return err
		},
		"ReadComment": func(f *canned) error {
			_, err := f.ReadComment(context.Background(), "acme/infra", "15")

			return err
		},
		"React": func(f *canned) error {
			return f.React(context.Background(), "acme/infra", "15", forge.ReactionEyes)
		},
	}
	keys := map[string]string{
		"UpsertComment": listCommentsAt + "1", "ReadComment": "GET /repos/acme/infra/issues/comments/15",
		"React": "POST /repos/acme/infra/issues/comments/15/reactions",
	}

	for name, call := range calls {
		for _, tt := range tests {
			t.Run(name+" "+tt.name, func(t *testing.T) {
				t.Parallel()

				f := cannedForge(t, map[string]hostAnswer{userPath: userAnswer(), keys[name]: tt.answer})

				err := call(f)
				if !errors.Is(err, tt.want) || err != nil && strings.Contains(err.Error(), cannedPAT) {
					t.Fatalf("%s = %v, want %v and no token in the text", name, err, tt.want)
				}
			})
		}
	}
}

// TestReadCommentAndReact checks the answers of the two calls.
func TestReadCommentAndReact(t *testing.T) {
	t.Parallel()

	f := cannedForge(t, map[string]hostAnswer{
		"GET /repos/acme/infra/issues/comments/15": okAnswer(commentJSON(15, "other-dev", 9, "Hello")),
		"GET /repos/acme/infra/issues/comments/16": okAnswer(commentJSON(17, "other-dev", 9, "Hello")),
		"POST /repos/acme/infra/issues/comments/15/reactions": {
			status: http.StatusCreated,
			body:   `{"id":1,"content":"eyes"}`,
			header: nil,
			next:   &hostAnswer{status: http.StatusOK, body: `{"id":1,"content":"eyes"}`, header: nil, next: nil},
		},
		"POST /repos/acme/infra/issues/comments/16/reactions": {
			status: http.StatusCreated,
			body:   `{"id":1,"content":"heart"}`,
			header: nil,
			next:   nil,
		},
	})

	got, err := f.ReadComment(t.Context(), "acme/infra", "15")
	if err != nil || got.ID != "15" || got.ReviewID != "7" || got.AuthorID != "9" || got.Body != "Hello" ||
		got.URL == "" {
		t.Fatalf("ReadComment = %+v, %v", got, err)
	}

	// An answer for another comment is not the comment that was asked for.
	if _, err := f.ReadComment(t.Context(), "acme/infra", "16"); !errors.Is(err, forge.ErrUnavailable) {
		t.Fatalf("ReadComment of an answer for another comment = %v, want ErrUnavailable", err)
	}

	// 201 for a new reaction and 200 for one that exists: both are nil.
	for i := range 2 {
		if err := f.React(t.Context(), "acme/infra", "15", forge.ReactionEyes); err != nil {
			t.Fatalf("React call %d = %v", i+1, err)
		}
	}

	if err := f.React(t.Context(), "acme/infra", "16", forge.ReactionEyes); !errors.Is(err, forge.ErrUnavailable) {
		t.Fatalf("React with an answer for another reaction = %v, want ErrUnavailable", err)
	}
}

// TestCommentTokenPermissions checks the permissions in the token request of an
// app: a read of a comment asks for the read of the issues, and the calls that
// write ask for the write of the issues, and for nothing else.
func TestCommentTokenPermissions(t *testing.T) {
	t.Parallel()

	const (
		read  = `{"repositories":["infra"],"permissions":{"issues":"read"}}`
		write = `{"repositories":["infra"],"permissions":{"issues":"write"}}`
	)

	tests := []struct {
		name string
		call func(ctx context.Context, f forge.Forge) error
		want string
	}{
		{"ReadComment", func(ctx context.Context, f forge.Forge) error {
			return second(f.ReadComment(ctx, conformanceRepo, "5"))
		}, read},
		{"React", func(ctx context.Context, f forge.Forge) error {
			return f.React(ctx, conformanceRepo, "5", forge.ReactionEyes)
		}, write},
		{"UpsertComment", func(ctx context.Context, f forge.Forge) error {
			return second(f.UpsertComment(ctx, conformanceRepo, "1", planRequest()))
		}, write},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			host := newHost(t)
			conn, cred := hostConnection(t, host, forge.AuthApp)

			f, err := newHostOpener(t, host).Open(t.Context(), conn, cred)
			if err != nil {
				t.Fatal(err)
			}

			// The comment or the review request does not exist; only the token request counts.
			if callErr := tt.call(t.Context(), f); !errors.Is(callErr, forge.ErrNotFound) {
				t.Fatalf("%s = %v, want ErrNotFound", tt.name, callErr)
			}

			if bodies := tokenBodies(host); len(bodies) != 1 || bodies[0] != tt.want {
				t.Fatalf("%s: the token requests = %v, want one: %s", tt.name, bodies, tt.want)
			}
		})
	}
}

// TestCommentCapabilityFollowsThePermissions checks that an installation with no
// permission to write the issues lists no comment capability after a check,
// and that the calls then make no request for a comment.
func TestCommentCapabilityFollowsThePermissions(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		permissions map[string]string
		want        bool
	}{
		{"issues write", map[string]string{"issues": "write", "metadata": "read"}, true},
		{"issues read", map[string]string{"issues": "read", "metadata": "read"}, false},
		{"pull requests write only", map[string]string{"pull_requests": "write", "metadata": "read"}, false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			o := openAppWith(t, tc.permissions)

			if _, err := o.fg.Check(t.Context()); err != nil {
				t.Fatalf("Check = %v", err)
			}

			if got := o.fg.Capabilities().Has(forge.CapComment); got != tc.want {
				t.Fatalf("after Check, comment listed = %v, want %v", got, tc.want)
			}

			before := len(o.host.Requests())
			_, err := o.fg.UpsertComment(t.Context(), conformanceRepo, "1", planRequest())

			if !tc.want && (!errors.Is(err, forge.ErrUnsupported) || len(o.host.Requests()) != before) {
				t.Fatalf(
					"UpsertComment = %v with %d calls, want ErrUnsupported and no call",
					err,
					len(o.host.Requests())-before,
				)
			}
		})
	}
}

// TestCommentIsCutBeforeTheCall checks the size of the text that goes to GitHub:
// a request above the limit is cut, so the body of the call never exceeds it.
func TestCommentIsCutBeforeTheCall(t *testing.T) {
	t.Parallel()

	host := newHostWith(t, func(c *githubfake.Config) {
		c.Permissions = map[string]string{
			"contents":      "write",
			"metadata":      "read",
			"pull_requests": "write",
			"issues":        "write",
		}
	})
	conn, cred := hostConnection(t, host, forge.AuthToken)

	f, err := newHostOpener(t, host).Open(t.Context(), conn, cred)
	if err != nil {
		t.Fatal(err)
	}

	c, err := host.Head(conformanceRepo, "main")
	if err != nil {
		t.Fatal(err)
	}

	_, pushErr := host.Push(
		conformanceRepo,
		"dustpan/j-1",
		map[string][]byte{"a": []byte("a")},
		"m",
	)

	if pushErr != nil {
		t.Fatal(pushErr)
	}

	review, err := f.OpenReview(
		t.Context(),
		conformanceRepo,
		forge.ReviewRequest{Branch: "dustpan/j-1", Target: "main", Title: "t", Body: "b"},
	)
	if err != nil {
		t.Fatalf("OpenReview = %v (main %s)", err, c.ID)
	}

	got, err := f.UpsertComment(t.Context(), conformanceRepo, review.ID,
		forge.CommentRequest{Marker: "plan", Body: strings.Repeat("x", 3*forge.MaxCommentBytes)})
	if err != nil || !got.Truncated || len(got.Body) > forge.MaxCommentBytes {
		t.Fatalf("UpsertComment = %d bytes, truncated %v, %v", len(got.Body), got.Truncated, err)
	}

	for _, r := range host.Requests() {
		if r.Method == http.MethodPost && strings.HasSuffix(r.URI, "/comments") &&
			len(r.Body) > forge.MaxCommentBytes+64 {
			t.Fatalf("the body of the call has %d bytes, want at most the limit and the field name", len(r.Body))
		}
	}
}

// TestUpsertCommentSkipsHostileComments checks that a comment of another account
// that is large or malformed does not stop the search: on a public repository,
// anyone can write such a comment.
func TestUpsertCommentSkipsHostileComments(t *testing.T) {
	t.Parallel()

	// 33000 characters of four bytes are a valid comment of GitHub, with more than
	// 131072 bytes.
	huge := commentJSON(11, "other-dev", 9, strings.Repeat("\U0001F600", 33000))
	noUser := map[string]any{"id": 12, "body": "no author", "html_url": "https://github.example/x"}
	badLogin := commentJSON(13, "not a login", 9, "text")
	textID := commentJSON(14, "other-dev", 9, "text")
	textID["id"] = "14"
	notObject := "a comment that is a text"
	forgedHuge := commentJSON(16, "other-dev", 9, markerPlan+"\n"+strings.Repeat("\U0001F600", 33000))
	mine := commentJSON(15, cannedLogin, 5, markerPlan+"\nOld")

	f := cannedForge(t, map[string]hostAnswer{
		userPath:             userAnswer(),
		listCommentsAt + "1": okAnswer([]any{huge, noUser, badLogin, textID, notObject, forgedHuge, mine}),
		"PATCH /repos/acme/infra/issues/comments/15": okAnswer(
			commentJSON(15, cannedLogin, 5, markerPlan+"\nPlan ready"),
		),
	})

	got, err := f.UpsertComment(t.Context(), "acme/infra", "7", planRequest())
	if err != nil || got.ID != "15" || f.sent(http.MethodPost) {
		t.Fatalf("UpsertComment = %+v, %v with the calls %v, want an edit of the comment 15", got, err, f.calls())
	}
}

// TestUpsertCommentSmallerPages checks that a page above the size limit of an
// answer does not stop the search: the adapter asks again for the same comments
// with a smaller page.
func TestUpsertCommentSmallerPages(t *testing.T) {
	t.Parallel()

	tooBig := statusAnswer(http.StatusOK, strings.Repeat(" ", 5<<20))
	mine := commentJSON(15, cannedLogin, 5, markerPlan+"\nOld")
	patch := "PATCH /repos/acme/infra/issues/comments/15"
	edited := okAnswer(commentJSON(15, cannedLogin, 5, markerPlan+"\nPlan ready"))
	at := func(size, page int) string {
		return fmt.Sprintf("GET /repos/acme/infra/issues/7/comments?per_page=%d&page=%d", size, page)
	}

	fill := func(from int) []any {
		items := make([]any, 0, 100)
		for i := range 100 {
			items = append(items, commentJSON(from+i, "other-dev", 9, "Comment"))
		}

		return items
	}

	t.Run("the first page", func(t *testing.T) {
		t.Parallel()

		f := cannedForge(t, map[string]hostAnswer{
			userPath: userAnswer(), at(100, 1): tooBig, at(25, 1): okAnswer([]any{mine}), patch: edited,
		})

		if got, err := f.UpsertComment(t.Context(), "acme/infra", "7", planRequest()); err != nil || got.ID != "15" {
			t.Fatalf("UpsertComment = %+v, %v; want an edit of the comment 15", got, err)
		}
	})

	t.Run("the second page keeps its offset", func(t *testing.T) {
		t.Parallel()

		f := cannedForge(t, map[string]hostAnswer{
			userPath: userAnswer(), at(100, 1): okAnswer(fill(100)), at(100, 2): tooBig,
			// The comments 101 to 125 are the pages 5 of 25 comments.
			at(25, 5): okAnswer([]any{mine}), patch: edited,
		})

		if got, err := f.UpsertComment(t.Context(), "acme/infra", "7", planRequest()); err != nil || got.ID != "15" {
			t.Fatalf("UpsertComment = %+v, %v; want an edit of the comment 15", got, err)
		}
	})

	t.Run("a comment above the limit with every size", func(t *testing.T) {
		t.Parallel()

		f := cannedForge(t, map[string]hostAnswer{
			userPath: userAnswer(), at(100, 1): tooBig, at(25, 1): tooBig, at(5, 1): tooBig, at(1, 1): tooBig,
		})

		_, err := f.UpsertComment(t.Context(), "acme/infra", "7", planRequest())
		if !errors.Is(err, forge.ErrUnavailable) || f.sent(http.MethodPost) {
			t.Fatalf("UpsertComment = %v with the calls %v, want ErrUnavailable and no comment", err, f.calls())
		}
	})
}
