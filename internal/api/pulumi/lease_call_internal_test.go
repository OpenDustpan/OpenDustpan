package pulumi

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/internal/token"
	"opendustpan.org/opendustpan/pkg/stack"
)

// TestLeaseCallHidesTheToken checks that no format and no log value of a
// LeaseCall shows the lease token. Seven lease handlers receive a LeaseCall,
// so a log line or an error that names one must never carry the secret.
func TestLeaseCallHidesTheToken(t *testing.T) {
	t.Parallel()

	tok, err := token.New()
	if err != nil {
		t.Fatalf("new token: %v", err)
	}

	call := LeaseCall{Token: tok, Ref: stack.Ref{Namespace: "acme", Project: "web", Stack: "dev"}, ID: "upd-1"}
	secret := tok.Reveal()

	var text, js bytes.Buffer

	slog.New(slog.NewTextHandler(&text, nil)).
		LogAttrs(context.Background(), slog.LevelInfo, "lease", slog.Any("call", call))
	slog.New(slog.NewJSONHandler(&js, nil)).
		LogAttrs(context.Background(), slog.LevelInfo, "lease", slog.Any("call", call))

	outputs := map[string]string{
		"%v":           fmt.Sprintf("%v", call),
		"%+v":          fmt.Sprintf("%+v", call),
		"%#v":          fmt.Sprintf("%#v", call),
		"%s":           fmt.Sprintf("%s", call),
		"text handler": text.String(),
		"JSON handler": js.String(),
	}

	for name, out := range outputs {
		if strings.Contains(out, secret) {
			t.Errorf("%s of a LeaseCall shows the lease token: %s", name, out)
		}
	}

	// The renew answer sends the token back to its client from the call.
	if call.Token.Reveal() != secret {
		t.Fatal("Token.Reveal does not return the lease token")
	}
}
