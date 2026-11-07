package forgetest

import (
	"errors"
	"strings"
	"testing"
	"unicode/utf8"

	"opendustpan.org/opendustpan/pkg/forge"
)

// reviewForComments opens a review request, or skips the case for a forge that
// cannot open one.
func reviewForComments(t *testing.T, h Harness) forge.Review {
	t.Helper()

	if !h.Forge.Capabilities().Has(forge.CapReview) {
		t.Skip("the case needs a review request, and the forge does not list the capability review")
	}

	return openReview(t, h, "dustpan/j-1")
}

// markerLine returns the marker line of the key and fails the test on an error.
func markerLine(t *testing.T, key string) string {
	t.Helper()

	line, err := forge.CommentMarker(key)
	if err != nil {
		t.Fatalf("CommentMarker(%q) = %v", key, err)
	}

	return line
}

// upsert calls UpsertComment and fails the test on an error.
func upsert(t *testing.T, h Harness, review, key, body string) forge.Comment {
	t.Helper()

	got, err := h.Forge.UpsertComment(t.Context(), h.Repo, review, forge.CommentRequest{Marker: key, Body: body})
	if err != nil {
		t.Fatalf("UpsertComment(%s, %q) = %v", review, key, err)
	}

	return got
}

// commentsOf returns the comments of the review request as the forge shows them.
func commentsOf(t *testing.T, h Harness, review string) []forge.Comment {
	t.Helper()

	got, err := h.Fixture.Comments(t.Context(), review)
	if err != nil {
		t.Fatalf("comments: %v", err)
	}

	return got
}

// bodiesByID maps the ID of each comment to its text.
func bodiesByID(comments []forge.Comment) map[string]string {
	out := make(map[string]string, len(comments))
	for _, c := range comments {
		out[c.ID] = c.Body
	}

	return out
}

func caseCommentUpsertEdits(t *testing.T, h Harness) {
	t.Helper()

	review := reviewForComments(t, h)
	plan := markerLine(t, "plan")

	first := upsert(t, h, review.ID, "plan", "Plan 1: 2 to add")
	if first.ID == "" || first.ReviewID != review.ID || first.Body != plan+"\nPlan 1: 2 to add" || first.Truncated ||
		first.URL == "" || first.AuthorID == "" {
		t.Fatalf("the first comment = %+v", first)
	}

	// The same key edits the comment: no second comment.
	second := upsert(t, h, review.ID, "plan", "Plan 2: 3 to add")
	if second.ID != first.ID || second.AuthorID != first.AuthorID || second.Body != plan+"\nPlan 2: 3 to add" {
		t.Fatalf("the second comment = %+v, want the comment %s with the new text", second, first.ID)
	}

	if shown := commentsOf(t, h, review.ID); len(shown) != 1 || shown[0].Body != second.Body {
		t.Fatalf("the forge shows %+v, want one comment with the new text", shown)
	}

	checkOtherKey(t, h, review.ID, first, second)
}

// checkOtherKey checks that another key makes another comment, that the first
// comment stays, and that ReadComment answers it.
func checkOtherKey(t *testing.T, h Harness, review string, first, second forge.Comment) {
	t.Helper()

	if apply := upsert(t, h, review, "apply", "Apply waits"); apply.ID == first.ID {
		t.Fatal("two keys made one comment")
	}

	if shown := commentsOf(t, h, review); len(shown) != 2 {
		t.Fatalf("the forge shows %d comments, want 2", len(shown))
	}

	read, err := h.Forge.ReadComment(t.Context(), h.Repo, first.ID)
	if err != nil {
		t.Fatalf("ReadComment = %v", err)
	}

	if read.ID != first.ID || read.Body != second.Body || read.ReviewID != review || read.AuthorID != first.AuthorID {
		t.Fatalf("ReadComment = %+v, want %+v", read, second)
	}
}

func caseCommentUpsertOwnOnly(t *testing.T, h Harness) {
	t.Helper()

	ctx := t.Context()
	review := reviewForComments(t, h)
	plan := markerLine(t, "plan")

	// A comment of a person with no marker, and a comment of a person that has
	// the marker as its first line.
	plain, err := h.Fixture.AddComment(ctx, review.ID, "Looks good to me")
	if err != nil {
		t.Fatalf("add a comment: %v", err)
	}

	forged, err := h.Fixture.AddComment(ctx, review.ID, plan+"\nforged text")
	if err != nil {
		t.Fatalf("add a comment: %v", err)
	}

	own := upsert(t, h, review.ID, "plan", "Plan ready")
	if own.ID == plain.ID || own.ID == forged.ID {
		t.Fatalf("UpsertComment edited the comment %s of a person", own.ID)
	}

	want := map[string]string{plain.ID: plain.Body, forged.ID: forged.Body, own.ID: plan + "\nPlan ready"}

	got := bodiesByID(commentsOf(t, h, review.ID))
	if len(got) != len(want) {
		t.Fatalf("the forge shows %v, want %v", got, want)
	}

	for id, body := range want {
		if got[id] != body {
			t.Errorf("the comment %s has the text %q, want %q", id, got[id], body)
		}
	}

	checkMarkerIsFirstLine(t, h, review.ID, own)
}

// checkMarkerIsFirstLine checks that a marker in a later line does not make a
// comment the one with that marker, and that the own comment is found again.
func checkMarkerIsFirstLine(t *testing.T, h Harness, review string, own forge.Comment) {
	t.Helper()

	late := upsert(t, h, review, "late", "Text\n"+markerLine(t, "plan"))
	if late.ID == own.ID {
		t.Fatal("a marker in a later line made the comment the one with that marker")
	}

	if again := upsert(t, h, review, "plan", "Plan ready again"); again.ID != own.ID {
		t.Fatalf("the second upsert made the comment %s, want the own comment %s", again.ID, own.ID)
	}
}

func caseCommentCut(t *testing.T, h Harness) {
	t.Helper()

	review := reviewForComments(t, h)

	// A text of exactly the limit is not cut, and a text of one byte more is.
	exactMarker := markerLine(t, "exact")
	exact := strings.Repeat("a", forge.MaxCommentBytes-len(exactMarker)-1)

	if got := upsert(t, h, review.ID, "exact", exact); got.Truncated || len(got.Body) != forge.MaxCommentBytes {
		t.Fatalf("a text at the limit: %d bytes, truncated %v", len(got.Body), got.Truncated)
	}

	if got := upsert(t, h, review.ID, "exact", exact+"a"); !got.Truncated || len(got.Body) > forge.MaxCommentBytes {
		t.Fatalf("a text one byte above the limit: %d bytes, truncated %v", len(got.Body), got.Truncated)
	}

	// A text of two-byte characters above the limit is cut at a character.
	got := upsert(t, h, review.ID, "plan", strings.Repeat("é", forge.MaxCommentBytes))
	checkCutText(t, got, markerLine(t, "plan"))

	// The forge holds the cut text, and the comment can be found again.
	if shown := bodiesByID(commentsOf(t, h, review.ID))[got.ID]; shown != got.Body {
		t.Fatalf("the forge shows %d bytes, want the %d bytes of the cut text", len(shown), len(got.Body))
	}

	again := upsert(t, h, review.ID, "plan", "Short")
	if again.ID != got.ID || again.Truncated || again.Body != markerLine(t, "plan")+"\nShort" {
		t.Fatalf("the edit after the cut = %+v", again)
	}
}

// checkCutText checks a comment that was cut: the size, the marker, the notice
// and the end of a character.
func checkCutText(t *testing.T, got forge.Comment, marker string) {
	t.Helper()

	if !got.Truncated || len(got.Body) > forge.MaxCommentBytes ||
		!strings.HasSuffix(got.Body, forge.CommentCutNotice) ||
		!strings.HasPrefix(got.Body, marker+"\n") ||
		!utf8.ValidString(got.Body) {
		t.Fatalf("the cut comment has %d bytes and truncated %v", len(got.Body), got.Truncated)
	}
}

func caseCommentInvalid(t *testing.T, h Harness) {
	t.Helper()

	ctx := t.Context()
	review := reviewForComments(t, h)
	before := callCount(ctx, t, h)

	checkInvalidUpserts(t, h, review.ID)
	checkInvalidIDs(t, h)

	if after := callCount(ctx, t, h); after != before {
		t.Fatalf("an invalid comment call made %d calls to the forge, want none", after-before)
	}

	if shown := commentsOf(t, h, review.ID); len(shown) != 0 {
		t.Fatalf("the forge shows %+v after invalid requests, want none", shown)
	}
}

func checkInvalidUpserts(t *testing.T, h Harness, review string) {
	t.Helper()

	ctx := t.Context()
	requests := []forge.CommentRequest{
		{Marker: "", Body: "text"},
		{Marker: "Plan", Body: "text"},
		{Marker: "-plan", Body: "text"},
		{Marker: "a b", Body: "text"},
		{Marker: "a-->b", Body: "text"},
		{Marker: "a\nb", Body: "text"},
		{Marker: strings.Repeat("a", forge.MaxCommentMarkerBytes+1), Body: "text"},
		{Marker: "plan", Body: ""},
		{Marker: "plan", Body: " \n\t"},
		{Marker: "plan", Body: "a\x00b"},
		{Marker: "plan", Body: "a\xffb"},
	}

	for _, req := range requests {
		if _, err := h.Forge.UpsertComment(ctx, h.Repo, review, req); !errors.Is(err, forge.ErrInvalid) {
			t.Errorf("UpsertComment with the request %+v = %v, want ErrInvalid", req, err)
		}
	}

	ok := forge.CommentRequest{Marker: "plan", Body: "text"}

	for _, id := range []string{"", "0", "01", "abc", "1/2", "-1", "12345678901"} {
		if _, err := h.Forge.UpsertComment(ctx, h.Repo, id, ok); !errors.Is(err, forge.ErrInvalid) {
			t.Errorf("UpsertComment on the review request %q = %v, want ErrInvalid", id, err)
		}
	}
}

func checkInvalidIDs(t *testing.T, h Harness) {
	t.Helper()

	ctx := t.Context()

	for _, id := range []string{"", "0", "abc", "1/2", "../1", strings.Repeat("9", 20)} {
		if _, err := h.Forge.ReadComment(ctx, h.Repo, id); !errors.Is(err, forge.ErrInvalid) {
			t.Errorf("ReadComment of %q = %v, want ErrInvalid", id, err)
		}

		if err := h.Forge.React(ctx, h.Repo, id, forge.ReactionEyes); !errors.Is(err, forge.ErrInvalid) {
			t.Errorf("React to %q = %v, want ErrInvalid", id, err)
		}
	}

	for _, r := range []forge.Reaction{"", "thumbsup", "+2", "EYES", "eyes\n"} {
		if err := h.Forge.React(ctx, h.Repo, "1", r); !errors.Is(err, forge.ErrInvalid) {
			t.Errorf("React with %q = %v, want ErrInvalid", string(r), err)
		}
	}
}

func caseCommentMissing(t *testing.T, h Harness) {
	t.Helper()

	ctx := t.Context()
	review := reviewForComments(t, h)
	ok := forge.CommentRequest{Marker: "plan", Body: "text"}

	if _, err := h.Forge.UpsertComment(ctx, h.Repo, "9999", ok); !errors.Is(err, forge.ErrNotFound) {
		t.Errorf("UpsertComment on a missing review request = %v, want ErrNotFound", err)
	}

	if _, err := h.Forge.ReadComment(ctx, h.Repo, "99999999"); !errors.Is(err, forge.ErrNotFound) {
		t.Errorf("ReadComment of a missing comment = %v, want ErrNotFound", err)
	}

	if err := h.Forge.React(ctx, h.Repo, "99999999", forge.ReactionEyes); !errors.Is(err, forge.ErrNotFound) {
		t.Errorf("React to a missing comment = %v, want ErrNotFound", err)
	}

	if shown := commentsOf(t, h, review.ID); len(shown) != 0 {
		t.Fatalf("the forge shows %+v, want none", shown)
	}
}

func caseCommentReact(t *testing.T, h Harness) {
	t.Helper()

	ctx := t.Context()
	review := reviewForComments(t, h)
	c := upsert(t, h, review.ID, "plan", "Plan ready")

	reactions := func() []string {
		t.Helper()

		got, err := h.Fixture.Reactions(ctx, c.ID)
		if err != nil {
			t.Fatalf("reactions: %v", err)
		}

		return got
	}

	// The same reaction twice is nil both times and shows once.
	for i := range 2 {
		if err := h.Forge.React(ctx, h.Repo, c.ID, forge.ReactionPlusOne); err != nil {
			t.Fatalf("React call %d = %v", i+1, err)
		}
	}

	if got := reactions(); len(got) != 1 || !strings.HasPrefix(got[0], "+1:") {
		t.Fatalf("the forge shows the reactions %v, want one +1", got)
	}

	if err := h.Forge.React(ctx, h.Repo, c.ID, forge.ReactionHeart); err != nil {
		t.Fatalf("React with heart = %v", err)
	}

	if got := reactions(); len(got) != 2 {
		t.Fatalf("the forge shows the reactions %v, want 2", got)
	}

	// A reaction does not change the text.
	if read, err := h.Forge.ReadComment(ctx, h.Repo, c.ID); err != nil || read.Body != c.Body {
		t.Fatalf("ReadComment after the reactions = %+v, %v", read, err)
	}
}
