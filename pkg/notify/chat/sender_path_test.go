package chat_test

import (
	"errors"
	"net/http"
	"net/url"
	"testing"

	"opendustpan.org/opendustpan/pkg/egress"
	"opendustpan.org/opendustpan/pkg/notify/chat"
	"opendustpan.org/opendustpan/pkg/notify/chat/matrix"
	"opendustpan.org/opendustpan/pkg/notify/chat/slack"
)

// TestRequestKeepsTheEncodedPath sends hostile bases straight to the builder of
// the request path, as if every check before it had failed. The request target
// keeps the encoded bytes of the URL: an encoded slash or an encoded dot never
// becomes a path separator or a dot segment, so no token can reach another path
// on the host.
func TestRequestKeepsTheEncodedPath(t *testing.T) {
	t.Parallel()

	const suffix = "/send/m.room.message/ntf-1"

	tests := []struct {
		name string
		base string
		want string
	}{
		{
			"an encoded room ID", "https://m.example.com/_matrix/client/v3/rooms/%21room%3Aexample.org",
			"/_matrix/client/v3/rooms/%21room%3Aexample.org" + suffix,
		},
		{"an encoded slash", "https://m.example.com/rooms/x%2F..%2F..%2Fadmin", "/rooms/x%2F..%2F..%2Fadmin" + suffix},
		{"encoded dots", "https://m.example.com/rooms/%2e%2e", "/rooms/%2e%2e" + suffix},
		{"an encoded dot", "https://m.example.com/rooms/%2E", "/rooms/%2E" + suffix},
		{"an encoded question mark", "https://m.example.com/rooms/a%3Fb", "/rooms/a%3Fb" + suffix},
		{"an encoded number sign", "https://m.example.com/rooms/a%23b", "/rooms/a%23b" + suffix},
		{"an encoded percent sign", "https://m.example.com/rooms/a%2541", "/rooms/a%2541" + suffix},
		{"a trailing slash", "https://m.example.com/rooms/r/", "/rooms/r" + suffix},
		{"an encoded space and a letter with accent", "https://m.example.com/a%20b/%C3%A9", "/a%20b/%C3%A9" + suffix},
		{"no path", "https://m.example.com", suffix},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			base, err := url.Parse(tt.base)
			if err != nil {
				t.Fatalf("Parse: %v", err)
			}

			req, err := chat.RequestForTest(base, chat.Request{
				Method: http.MethodPut, PathSuffix: suffix, IdempotencyKey: "ntf-1", ContentType: "application/json",
				Body: []byte("{}"),
			})
			if err != nil {
				t.Fatalf("request: %v", err)
			}

			if got := req.URL.RequestURI(); got != tt.want {
				t.Errorf("request target = %q, want %q", got, tt.want)
			}
		})
	}
}

// TestSenderSendsTheBytesOfTheURLWithNoSuffix checks that a renderer with no
// path suffix, as Slack, Mattermost and Teams have, sends the path of the URL
// as it is, encoded bytes included.
func TestSenderSendsTheBytesOfTheURLWithNoSuffix(t *testing.T) {
	t.Parallel()

	recv := newReceiver(t)
	s := newAuthSender(t, recv, slack.New())

	m := message()
	m.To = "https://hooks.slack.com/services/T0/B0/a%20b%C3%A9%2B%3A"

	if err := s.Send(t.Context(), m); err != nil {
		t.Fatalf("Send: %v", err)
	}

	got := recv.requests()
	if len(got) != 1 || got[0].uri != "/services/T0/B0/a%20b%C3%A9%2B%3A" {
		t.Fatalf("requests = %+v, want one with the target of the URL as it is", got)
	}
}

// TestSenderRefusesAPathThatLeavesItsPlace sends hostile URLs to the sender, as
// if the sink service had not checked them. The sender must refuse each one
// with egress.ErrRefused, send nothing, and so keep the token of the sink from
// every other path on the host.
func TestSenderRefusesAPathThatLeavesItsPlace(t *testing.T) {
	t.Parallel()

	segments := []string{
		"x%2F..%2F..%2Fadmin", "%2e%2e", "%2E%2E", "%2e", "..", ".", "a%2fb", "a%5Cb", "a%5cb", "a%3Fb", "a%23b",
		"a%00b", "a%0Ab", "a%7F", "%zz",
	}

	for _, seg := range segments {
		t.Run(seg, func(t *testing.T) {
			t.Parallel()

			recv := newReceiver(t)
			s := newAuthSender(t, recv, matrix.New())

			m := authMessage(authCanary)
			m.Sink.Platform, m.Sink.Target = "matrix", "chat.example.com"
			m.To = "https://chat.example.com/_matrix/client/v3/rooms/" + seg

			if err := s.Send(t.Context(), m); !errors.Is(err, egress.ErrRefused) {
				t.Errorf("Send with the segment %q = %v, want egress.ErrRefused", seg, err)
			}

			if got := recv.requests(); len(got) != 0 {
				t.Errorf("the server got %d requests for the segment %q, want none: %+v", len(got), seg, got)
			}
		})
	}
}
