package agenttest

import (
	"bytes"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/agent"
)

// RunRevealSealer runs the sealer suite against the RevealSealer that f
// makes.
func RunRevealSealer(t *testing.T, f func(t *testing.T) agent.RevealSealer) {
	t.Helper()

	cases := []struct {
		name string
		run  func(t *testing.T, s agent.RevealSealer)
	}{
		{name: "seal and open", run: caseSealerRoundTrip},
		{name: "a seal holds no plain value", run: caseSealerNoPlain},
		{name: "each seal has its own data key", run: caseSealerOwnKey},
		{name: "a changed reveal does not open", run: caseSealerTampered},
		{name: "a reveal that is not valid", run: caseSealerInvalid},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			c.run(t, f(t))
		})
	}
}

// sealerValue is the plain value of the reveals of the suite. It is no
// credential of any system.
const sealerValue = "agenttest-sealer-value-42"

// PlainRevealFor returns a plain reveal of call 1 of turn 1 of run.
func PlainRevealFor(run agent.RunID) agent.PlainReveal {
	now := time.Now()

	return agent.PlainReveal{
		Run: run, Turn: 1, CallID: "call-1",
		Content: []byte(`{"resources":[{"attributes":{"password":"` + sealerValue + `"}}]}`),
		Values:  []string{sealerValue}, At: now, ExpiresAt: agent.RevealExpiry(now, agent.DefaultLimits()),
	}
}

func mustSeal(t *testing.T, s agent.RevealSealer, p agent.PlainReveal) agent.SealedReveal {
	t.Helper()

	sealed, err := s.Seal(t.Context(), p)
	if err != nil {
		t.Fatalf("Seal() = %v", err)
	}

	return sealed
}

func caseSealerRoundTrip(t *testing.T, s agent.RevealSealer) {
	t.Helper()

	p := PlainRevealFor("run-1")
	sealed := mustSeal(t, s, p)

	if sealed.Run != p.Run || sealed.Turn != p.Turn || sealed.CallID != p.CallID || !sealed.At.Equal(p.At) ||
		!sealed.ExpiresAt.Equal(p.ExpiresAt) || len(sealed.WrappedKey) == 0 {
		t.Fatalf("Seal() = %+v, want the run, turn, call and times of the reveal and a wrapped key", sealed)
	}

	got, err := s.Open(t.Context(), sealed)
	if err != nil || !bytes.Equal(got.Content, p.Content) || !slices.Equal(got.Values, p.Values) {
		t.Fatalf("Open() = %+v, %v, want the plain reveal", got, err)
	}
}

func caseSealerNoPlain(t *testing.T, s agent.RevealSealer) {
	t.Helper()

	sealed := mustSeal(t, s, PlainRevealFor("run-1"))

	for name, b := range map[string][]byte{
		"key": sealed.WrappedKey, "content": sealed.Content, "values": sealed.Values,
	} {
		if bytes.Contains(b, []byte(sealerValue)) {
			t.Fatalf("the sealed %s holds the plain value", name)
		}
	}
}

func caseSealerOwnKey(t *testing.T, s agent.RevealSealer) {
	t.Helper()

	first, second := mustSeal(t, s, PlainRevealFor("run-1")), mustSeal(t, s, PlainRevealFor("run-1"))
	if bytes.Equal(first.WrappedKey, second.WrappedKey) || bytes.Equal(first.Content, second.Content) {
		t.Fatal("two seals of one reveal have one key or one content, want a fresh data key for each seal")
	}
}

func caseSealerTampered(t *testing.T, s agent.RevealSealer) {
	t.Helper()

	sealed := mustSeal(t, s, PlainRevealFor("run-1"))
	other := mustSeal(t, s, PlainRevealFor("run-2"))

	for name, change := range map[string]func(r *agent.SealedReveal){
		"another run":           func(r *agent.SealedReveal) { r.Run = "run-2" },
		"another turn":          func(r *agent.SealedReveal) { r.Turn = 2 },
		"another call":          func(r *agent.SealedReveal) { r.CallID = "call-2" },
		"the key of another":    func(r *agent.SealedReveal) { r.WrappedKey = other.WrappedKey },
		"a changed content":     func(r *agent.SealedReveal) { r.Content = flipLast(r.Content) },
		"the values of another": func(r *agent.SealedReveal) { r.Values = other.Values },
	} {
		r := sealed
		r.WrappedKey, r.Content, r.Values = slices.Clone(r.WrappedKey), slices.Clone(r.Content), slices.Clone(r.Values)
		change(&r)

		got, err := s.Open(t.Context(), r)
		if !errors.Is(err, agent.ErrRevealOpen) || len(got.Content) != 0 || len(got.Values) != 0 {
			t.Fatalf("Open() with %s = %d bytes, %v, want ErrRevealOpen and nothing", name, len(got.Content), err)
		}

		if err != nil && strings.Contains(err.Error(), sealerValue) {
			t.Fatalf("Open() with %s answered the plain value in its error", name)
		}
	}
}

// flipLast returns b with its last bit flipped.
func flipLast(b []byte) []byte {
	out := slices.Clone(b)
	if len(out) > 0 {
		out[len(out)-1] ^= 1
	}

	return out
}

func caseSealerInvalid(t *testing.T, s agent.RevealSealer) {
	t.Helper()

	for name, change := range map[string]func(p *agent.PlainReveal){
		"no run":                       func(p *agent.PlainReveal) { p.Run = "" },
		"no call":                      func(p *agent.PlainReveal) { p.CallID = "" },
		"no content":                   func(p *agent.PlainReveal) { p.Content = nil },
		"no value":                     func(p *agent.PlainReveal) { p.Values = nil },
		"an expiry at the reveal time": func(p *agent.PlainReveal) { p.ExpiresAt = p.At },
	} {
		p := PlainRevealFor("run-1")
		change(&p)

		if _, err := s.Seal(t.Context(), p); !errors.Is(err, agent.ErrInvalid) {
			t.Fatalf("Seal() with %s = %v, want ErrInvalid", name, err)
		}
	}
}
