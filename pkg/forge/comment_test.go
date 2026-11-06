package forge_test

import (
	"errors"
	"strings"
	"testing"
	"unicode/utf8"

	"opendustpan.org/opendustpan/pkg/forge"
)

func TestCommentMarker(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		key  string
		want error
	}{
		{"a word", "plan", nil},
		{"a key with a digit, a dot, a dash and an underscore", "plan-2.a_b", nil},
		{"a key at the limit", strings.Repeat("a", forge.MaxCommentMarkerBytes), nil},
		{"no key", "", forge.ErrInvalid},
		{"a key above the limit", strings.Repeat("a", forge.MaxCommentMarkerBytes+1), forge.ErrInvalid},
		{"a capital letter", "Plan", forge.ErrInvalid},
		{"a space", "a b", forge.ErrInvalid},
		{"the end of an HTML comment", "a-->b", forge.ErrInvalid},
		{"a line break", "a\nb", forge.ErrInvalid},
		{"a key that starts with a dash", "-a", forge.ErrInvalid},
		{"a key that starts with a dot", ".a", forge.ErrInvalid},
		{"a key that starts with an underscore", "_a", forge.ErrInvalid},
		{"a letter of another script", "pläne", forge.ErrInvalid},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			line, err := forge.CommentMarker(tt.key)
			if tt.want == nil {
				if err != nil || line != "<!-- opendustpan:"+tt.key+" -->" {
					t.Fatalf("CommentMarker = %q, %v", line, err)
				}

				return
			}

			if !errors.Is(err, tt.want) || line != "" {
				t.Fatalf("CommentMarker = %q, %v; want %v", line, err, tt.want)
			}
		})
	}
}

func TestValidateComment(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		req  forge.CommentRequest
		want error
	}{
		{"a valid request", forge.CommentRequest{Marker: "plan", Body: "text"}, nil},
		{"a body with line breaks and a tab", forge.CommentRequest{Marker: "plan", Body: "a\r\nb\tc"}, nil},
		{
			"a body above the limit",
			forge.CommentRequest{Marker: "plan", Body: strings.Repeat("a", 2*forge.MaxCommentBytes)},
			nil,
		},
		{"a bad key", forge.CommentRequest{Marker: "", Body: "text"}, forge.ErrInvalid},
		{"no body", forge.CommentRequest{Marker: "plan", Body: ""}, forge.ErrInvalid},
		{"a body of spaces", forge.CommentRequest{Marker: "plan", Body: " \n "}, forge.ErrInvalid},
		{"a null byte", forge.CommentRequest{Marker: "plan", Body: "a\x00b"}, forge.ErrInvalid},
		{"an escape character", forge.CommentRequest{Marker: "plan", Body: "a\x1bb"}, forge.ErrInvalid},
		{"a body that is not UTF-8", forge.CommentRequest{Marker: "plan", Body: "a\xffb"}, forge.ErrInvalid},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			err := forge.ValidateComment(tt.req)
			if tt.want == nil && err != nil || tt.want != nil && !errors.Is(err, tt.want) {
				t.Fatalf("ValidateComment = %v, want %v", err, tt.want)
			}
		})
	}
}

// commentTextCase is one row of the test of CommentText.
type commentTextCase struct {
	name    string
	body    string
	wantCut bool
}

func TestCommentText(t *testing.T) {
	t.Parallel()

	room := forge.MaxCommentBytes - len("<!-- opendustpan:plan -->") - 1

	tests := []commentTextCase{
		{"a short text", "Plan ready", false},
		{"a text of exactly the limit", strings.Repeat("a", room), false},
		{"a text of one byte more", strings.Repeat("a", room+1), true},
		{"a text of two-byte characters", strings.Repeat("é", forge.MaxCommentBytes), true},
		{"a text of four-byte characters", strings.Repeat("😀", forge.MaxCommentBytes), true},
		{
			"a text of three-byte characters in a row of one byte",
			"a" + strings.Repeat("€", forge.MaxCommentBytes),
			true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			checkCommentText(t, tt)
		})
	}

	_, _, err := forge.CommentText(forge.CommentRequest{Marker: "Bad", Body: "text"})
	if !errors.Is(err, forge.ErrInvalid) {
		t.Fatalf("CommentText with a bad key = %v, want ErrInvalid", err)
	}
}

// checkCommentText checks the text that CommentText makes for a body.
func checkCommentText(t *testing.T, tt commentTextCase) {
	t.Helper()

	marker := "<!-- opendustpan:plan -->"

	body := tt.body

	text, cut, err := forge.CommentText(forge.CommentRequest{Marker: "plan", Body: body})
	if err != nil || cut != tt.wantCut {
		t.Fatalf("CommentText = %d bytes, cut %v, %v; want cut %v", len(text), cut, err, tt.wantCut)
	}

	if len(text) > forge.MaxCommentBytes || !utf8.ValidString(text) || !strings.HasPrefix(text, marker+"\n") {
		t.Fatalf("CommentText has %d bytes, valid UTF-8 %v", len(text), utf8.ValidString(text))
	}

	if cut != strings.HasSuffix(text, forge.CommentCutNotice) {
		t.Fatalf("the notice and the cut disagree: cut %v", cut)
	}

	if !cut && text != marker+"\n"+body {
		t.Fatal("a text that is not cut changed")
	}
}

func TestValidateReaction(t *testing.T) {
	t.Parallel()

	for _, r := range []forge.Reaction{
		forge.ReactionPlusOne, forge.ReactionMinusOne, forge.ReactionLaugh, forge.ReactionConfused, forge.ReactionHeart,
		forge.ReactionHooray, forge.ReactionRocket, forge.ReactionEyes,
	} {
		if err := forge.ValidateReaction(r); err != nil {
			t.Errorf("ValidateReaction(%q) = %v", string(r), err)
		}
	}

	for _, r := range []forge.Reaction{"", "EYES", "thumbsup", "eyes ", "+2"} {
		if err := forge.ValidateReaction(r); !errors.Is(err, forge.ErrInvalid) {
			t.Errorf("ValidateReaction(%q) = %v, want ErrInvalid", string(r), err)
		}
	}
}
