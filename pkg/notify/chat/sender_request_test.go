package chat_test

import (
	"errors"
	"net/http"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/pkg/notify"
	"opendustpan.org/opendustpan/pkg/notify/chat"
	"opendustpan.org/opendustpan/pkg/notify/chat/slack"
)

// hostileRenderer is a renderer that breaks the contract of Request. It takes
// the host rule and the answer rule of Slack, and returns req from Render.
type hostileRenderer struct {
	slack.Renderer

	req chat.Request
}

func (h hostileRenderer) Render(chat.Content) (chat.Request, error) { return h.req, nil }

// TestSenderRefusesARequestThatBreaksTheContract sends a hostile Request to
// the sender, as if the conformance suite had never run on the renderer. The
// sender must refuse it with ErrPermanent and open no connection.
func TestSenderRefusesARequestThatBreaksTheContract(t *testing.T) {
	t.Parallel()

	tests := requestCases()

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			checkRequestCase(t, tt)
		})
	}
}

// checkRequestCase sends tt.req through the sender and checks the outcome.
func checkRequestCase(t *testing.T, tt requestCase) {
	t.Helper()

	recv := newReceiver(t)

	s, err := chat.NewForTest(recv.srv, now, hostileRenderer{Renderer: slack.New(), req: tt.req})
	if err != nil {
		t.Fatalf("NewForTest: %v", err)
	}

	t.Cleanup(s.Close)

	err = s.Send(t.Context(), message())
	got := recv.requests()

	if tt.ok {
		if err != nil || len(got) != 1 {
			t.Fatalf("Send = %v with %d requests, want nil and 1", err, len(got))
		}

		return
	}

	if !errors.Is(err, notify.ErrPermanent) {
		t.Errorf("Send = %v, want ErrPermanent", err)
	}

	if err != nil && strings.Contains(err.Error(), canaryToken) {
		t.Errorf("the error text holds the token: %v", err)
	}

	if len(got) != 0 {
		t.Errorf("the server got %d requests, want none: %+v", len(got), got)
	}
}

type requestCase struct {
	name string
	req  chat.Request
	ok   bool
}

// requestCases returns requests that the sender takes, and requests that
// break the contract of Request.
func requestCases() []requestCase {
	valid := chat.Request{
		Method: http.MethodPost, PathSuffix: "", IdempotencyKey: "ntf-1", ContentType: "application/json",
		Body: []byte(`{"text":"x"}`),
	}

	with := func(mutate func(r *chat.Request)) chat.Request {
		r := valid
		mutate(&r)

		return r
	}

	return []requestCase{
		{"the valid request", valid, true},
		{"a valid PUT", with(func(r *chat.Request) { r.Method = http.MethodPut }), true},
		{"a valid path suffix", with(func(r *chat.Request) { r.PathSuffix = "/a/b-c_d" }), true},
		{"a body of the largest size", with(func(r *chat.Request) {
			r.Body = []byte(strings.Repeat("x", chat.MaxRequestBytes))
		}), true},
		{"a path suffix of the longest size", with(func(r *chat.Request) {
			r.PathSuffix = "/" + strings.Repeat("a", chat.MaxPathSuffixLen-1)
		}), true},

		{"no method", with(func(r *chat.Request) { r.Method = "" }), false},
		{"GET", with(func(r *chat.Request) { r.Method = http.MethodGet }), false},
		{"DELETE", with(func(r *chat.Request) { r.Method = http.MethodDelete }), false},
		{"a method in lower case", with(func(r *chat.Request) { r.Method = "post" }), false},
		{"a method with a line feed", with(func(r *chat.Request) { r.Method = "POST\nHost: evil.example" }), false},
		{"no content type", with(func(r *chat.Request) { r.ContentType = "" }), false},
		{"no body", with(func(r *chat.Request) { r.Body = nil }), false},
		{"an empty body", with(func(r *chat.Request) { r.Body = []byte{} }), false},
		{"a body above the limit", with(func(r *chat.Request) {
			r.Body = []byte(strings.Repeat("x", chat.MaxRequestBytes+1))
		}), false},
		{"a path suffix above the limit", with(func(r *chat.Request) {
			r.PathSuffix = "/" + strings.Repeat("a", chat.MaxPathSuffixLen)
		}), false},
		{"a suffix with no leading slash", with(func(r *chat.Request) { r.PathSuffix = "a/b" }), false},
		{"a suffix for another host", with(func(r *chat.Request) { r.PathSuffix = "@evil.example/" }), false},
		{"a suffix with two leading slashes", with(func(r *chat.Request) { r.PathSuffix = "//evil.example/x" }), false},
		{"a dot segment", with(func(r *chat.Request) { r.PathSuffix = "/../admin" }), false},
		{"a dot segment at the end", with(func(r *chat.Request) { r.PathSuffix = "/a/.." }), false},
		{"two dots in a name", with(func(r *chat.Request) { r.PathSuffix = "/a..b" }), false},
		{"a query", with(func(r *chat.Request) { r.PathSuffix = "/a?token=x" }), false},
		{"a fragment", with(func(r *chat.Request) { r.PathSuffix = "/a#b" }), false},
		{"a backslash", with(func(r *chat.Request) { r.PathSuffix = `/a\b` }), false},
		{"a colon", with(func(r *chat.Request) { r.PathSuffix = "/a:b" }), false},
		{"a space", with(func(r *chat.Request) { r.PathSuffix = "/a b" }), false},
		{"a tab", with(func(r *chat.Request) { r.PathSuffix = "/a\tb" }), false},
		{"a line feed", with(func(r *chat.Request) { r.PathSuffix = "/a\nHost: evil.example" }), false},
		{"a carriage return", with(func(r *chat.Request) { r.PathSuffix = "/a\rb" }), false},
		{"a NUL byte", with(func(r *chat.Request) { r.PathSuffix = "/a\x00b" }), false},
		{"a DEL byte", with(func(r *chat.Request) { r.PathSuffix = "/a\x7fb" }), false},
	}
}
