package chat_test

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/pkg/notify/chat/matrix"
)

// TestSenderPostsAMatrixEvent sends through the real sender with the Matrix
// renderer: a PUT to the URL of the room with the send path and the
// notification ID as the transaction ID, the access token as a bearer header,
// and no token in the body.
func TestSenderPostsAMatrixEvent(t *testing.T) {
	t.Parallel()

	recv := newReceiver(t)
	recv.set(reply(http.StatusOK, `{"event_id":"$e1:example.org"}`))

	s := newAuthSender(t, recv, matrix.New())

	m := authMessage(authCanary)
	m.Sink.Platform, m.Sink.Target = "matrix", "chat.example.com"
	m.To = "https://chat.example.com/_matrix/client/v3/rooms/%21room%3Aexample.org"

	if err := s.Send(t.Context(), m); err != nil {
		t.Fatalf("Send: %v", err)
	}

	got := recv.requests()
	if len(got) != 1 {
		t.Fatalf("the server got %d requests, want 1", len(got))
	}

	r := got[0]
	if r.method != http.MethodPut || r.path != "/_matrix/client/v3/rooms/!room:example.org/send/m.room.message/ntf-1" ||
		r.header.Get("Authorization") != "Bearer "+authCanary {
		t.Errorf("request = %s %s with Authorization %q", r.method, r.path, r.header.Get("Authorization"))
	}

	var ev struct {
		Msgtype string `json:"msgtype"`
	}
	if err := json.Unmarshal(r.body, &ev); err != nil || ev.Msgtype != "m.text" {
		t.Errorf("body = %q (%v), want an m.text event", r.body, err)
	}

	if strings.Contains(string(r.body), authCanary) {
		t.Error("the body holds the access token")
	}
}

// TestSenderRefusesAMatrixSinkWithoutAToken checks that the sender sends
// nothing for a Matrix sink that has no access token.
func TestSenderRefusesAMatrixSinkWithoutAToken(t *testing.T) {
	t.Parallel()

	recv := newReceiver(t)
	s := newAuthSender(t, recv, matrix.New())

	m := message()
	m.Sink.Platform, m.Sink.Target = "matrix", "chat.example.com"
	m.To = "https://chat.example.com/_matrix/client/v3/rooms/room"

	if err := s.Send(t.Context(), m); err == nil {
		t.Fatal("Send = nil, want an error")
	}

	if got := recv.requests(); len(got) != 0 {
		t.Errorf("the server got %d requests, want none", len(got))
	}
}
