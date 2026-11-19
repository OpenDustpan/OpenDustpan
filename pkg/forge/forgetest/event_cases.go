package forgetest

import (
	"bytes"
	"errors"
	"testing"

	"opendustpan.org/opendustpan/pkg/forge"
)

// otherEventSecret returns a second event secret of 32 bytes that is not the
// secret of the harness.
func otherEventSecret(h Harness) []byte {
	other := bytes.Repeat([]byte("b"), forge.MinEventSecretBytes)
	if bytes.Equal(other, h.EventSecret) {
		other = bytes.Repeat([]byte("c"), forge.MinEventSecretBytes)
	}

	return other
}

// parsed makes an event of kind that the forge signs with the secret of the
// harness, and parses it with that secret.
func parsed(t *testing.T, h Harness, kind forge.EventKind) forge.Event {
	t.Helper()

	req, err := h.Fixture.Event(t.Context(), kind, h.EventSecret)
	if err != nil {
		t.Fatalf("make the event: %v", err)
	}

	ev, err := h.Forge.ParseEvent(t.Context(), req, [][]byte{h.EventSecret})
	if err != nil {
		t.Fatalf("ParseEvent = %v", err)
	}

	if ev.DeliveryID == "" {
		t.Fatal("the event has no delivery ID")
	}

	return ev
}

func caseEventPing(t *testing.T, h Harness) {
	t.Helper()

	if ev := parsed(t, h, forge.EventPing); ev.Kind != forge.EventPing {
		t.Fatalf("Kind = %q, want ping", ev.Kind)
	}
}

func caseEventReview(t *testing.T, h Harness) {
	t.Helper()

	ev := parsed(t, h, forge.EventReview)

	if ev.Kind != forge.EventReview || ev.Repo != h.Repo || ev.ReviewID == "" || ev.Sender.ID == "" ||
		ev.Sender.Login == "" {
		t.Fatalf("event = %+v, want a review event of %s with a review ID and a sender", ev, h.Repo)
	}
}

func caseEventNoSecret(t *testing.T, h Harness) {
	t.Helper()

	req, err := h.Fixture.Event(t.Context(), forge.EventReview, h.EventSecret)
	if err != nil {
		t.Fatalf("make the event: %v", err)
	}

	short := h.EventSecret[:forge.MinEventSecretBytes-1]

	shortReq, err := h.Fixture.Event(t.Context(), forge.EventReview, short)
	if err != nil {
		t.Fatalf("make the event with a short secret: %v", err)
	}

	for name, secrets := range map[string][][]byte{
		"a nil list": nil, "an empty list": {}, "a nil secret": {nil}, "an empty secret": {{}},
		"a short secret": {short},
	} {
		got := req
		if name == "a short secret" {
			got = shortReq
		}

		if _, parseErr := h.Forge.ParseEvent(t.Context(), got, secrets); !errors.Is(parseErr, forge.ErrSignature) {
			t.Errorf("ParseEvent with %s = %v, want ErrSignature", name, parseErr)
		}
	}
}

// requireParseError checks that ParseEvent of req against secrets fails with
// want.
func requireParseError(t *testing.T, h Harness, req forge.EventRequest, secrets [][]byte, want error) {
	t.Helper()

	if _, err := h.Forge.ParseEvent(t.Context(), req, secrets); !errors.Is(err, want) {
		t.Errorf("ParseEvent = %v, want %v", err, want)
	}
}

func caseEventWrongSignature(t *testing.T, h Harness) {
	t.Helper()

	req, err := h.Fixture.Event(t.Context(), forge.EventReview, h.EventSecret)
	if err != nil {
		t.Fatalf("make the event: %v", err)
	}

	requireParseError(t, h, req, [][]byte{otherEventSecret(h)}, forge.ErrSignature)
}

func caseEventChangedBody(t *testing.T, h Harness) {
	t.Helper()

	req, err := h.Fixture.Event(t.Context(), forge.EventReview, h.EventSecret)
	if err != nil {
		t.Fatalf("make the event: %v", err)
	}

	req.Body = append(bytes.Clone(req.Body), ' ')

	requireParseError(t, h, req, [][]byte{h.EventSecret}, forge.ErrSignature)
}

func caseEventBadBody(t *testing.T, h Harness) {
	t.Helper()

	for _, kind := range []forge.EventKind{forge.EventPing, forge.EventReview} {
		req, err := h.Fixture.RawEvent(t.Context(), kind, []byte("this is not JSON"), h.EventSecret)
		if err != nil {
			t.Fatalf("make the event: %v", err)
		}

		requireParseError(t, h, req, [][]byte{h.EventSecret}, forge.ErrInvalid)
	}
}

func caseEventOutsidePort(t *testing.T, h Harness) {
	t.Helper()

	req, err := h.Fixture.Event(t.Context(), forge.EventKind("push"), h.EventSecret)
	if err != nil {
		t.Fatalf("make the event: %v", err)
	}

	requireParseError(t, h, req, [][]byte{h.EventSecret}, forge.ErrUnsupported)
}

func caseEventInstallation(t *testing.T, h Harness) {
	t.Helper()

	if h.Connection.Auth != forge.AuthApp {
		t.Skip("only an app has installations")
	}

	ev := parsed(t, h, forge.EventReview)

	if want := h.Connection.Params[forge.ParamInstallationID]; ev.InstallationID == "" || ev.InstallationID != want {
		t.Fatalf("InstallationID = %q, want %q", ev.InstallationID, want)
	}
}

func caseEventTwoSecrets(t *testing.T, h Harness) {
	t.Helper()

	both := [][]byte{h.EventSecret, otherEventSecret(h)}

	for _, secret := range both {
		req, err := h.Fixture.Event(t.Context(), forge.EventReview, secret)
		if err != nil {
			t.Fatalf("make the event: %v", err)
		}

		if _, parseErr := h.Forge.ParseEvent(t.Context(), req, both); parseErr != nil {
			t.Errorf("ParseEvent of an event signed with one of two secrets = %v", parseErr)
		}
	}
}

func caseEventRetry(t *testing.T, h Harness) {
	t.Helper()

	first, err := h.Fixture.Event(t.Context(), forge.EventReview, h.EventSecret)
	if err != nil {
		t.Fatalf("make the event: %v", err)
	}

	again, err := h.Fixture.Redeliver(t.Context(), first)
	if err != nil {
		t.Fatalf("redeliver: %v", err)
	}

	one, err := h.Forge.ParseEvent(t.Context(), first, [][]byte{h.EventSecret})
	if err != nil {
		t.Fatalf("ParseEvent = %v", err)
	}

	two, err := h.Forge.ParseEvent(t.Context(), again, [][]byte{h.EventSecret})
	if err != nil {
		t.Fatalf("ParseEvent of the retry = %v", err)
	}

	if one.DeliveryID == "" || one.DeliveryID != two.DeliveryID || !bytes.Equal(first.Body, again.Body) {
		t.Fatalf("delivery IDs %q and %q, want the same non-empty ID and the same body", one.DeliveryID, two.DeliveryID)
	}
}
