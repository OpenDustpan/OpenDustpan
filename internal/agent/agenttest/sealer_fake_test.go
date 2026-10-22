package agenttest_test

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"testing"

	"opendustpan.org/opendustpan/internal/agent"
	"opendustpan.org/opendustpan/internal/agent/agenttest"
)

func TestRevealSealerSuite(t *testing.T) {
	t.Parallel()

	agenttest.RunRevealSealer(t, func(t *testing.T) agent.RevealSealer {
		t.Helper()

		kek := make([]byte, 32)
		if _, err := rand.Read(kek); err != nil {
			t.Fatalf("make a key: %v", err)
		}

		return fakeSealer{kek: kek}
	})
}

// fakeSealer seals with AES-GCM: a fresh data key for each reveal, wrapped
// with kek. The run, the turn and the call are the additional data of each
// seal. It stands in for the sealer of package secret.
type fakeSealer struct {
	kek []byte
}

func (s fakeSealer) Seal(_ context.Context, p agent.PlainReveal) (agent.SealedReveal, error) {
	var zero agent.SealedReveal

	if p.Run == "" || p.CallID == "" || len(p.Content) == 0 || len(p.Values) == 0 || !p.ExpiresAt.After(p.At) {
		return zero, agent.ErrInvalid
	}

	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		return zero, fmt.Errorf("make a data key: %w", err)
	}

	values, err := json.Marshal(p.Values)
	if err != nil {
		return zero, fmt.Errorf("encode the values: %w", err)
	}

	aad := revealAAD(p.Run, p.Turn, p.CallID)
	wrapped, err1 := seal(s.kek, key, aad)
	content, err2 := seal(key, p.Content, append(aad, 'c'))
	sealedValues, err3 := seal(key, values, append(aad, 'v'))

	if err1 != nil || err2 != nil || err3 != nil {
		return zero, fmt.Errorf("seal the reveal: %w", agent.ErrInvalid)
	}

	return agent.SealedReveal{
		Run: p.Run, Turn: p.Turn, CallID: p.CallID, WrappedKey: wrapped, Content: content, Values: sealedValues,
		At: p.At, ExpiresAt: p.ExpiresAt,
	}, nil
}

func (s fakeSealer) Open(_ context.Context, r agent.SealedReveal) (agent.PlainReveal, error) {
	var zero agent.PlainReveal

	aad := revealAAD(r.Run, r.Turn, r.CallID)

	key, err := open(s.kek, r.WrappedKey, aad)
	if err != nil {
		return zero, agent.ErrRevealOpen
	}

	content, err := open(key, r.Content, append(aad, 'c'))
	if err != nil {
		return zero, agent.ErrRevealOpen
	}

	raw, err := open(key, r.Values, append(aad, 'v'))
	if err != nil {
		return zero, agent.ErrRevealOpen
	}

	var values []string
	if err := json.Unmarshal(raw, &values); err != nil {
		return zero, agent.ErrRevealOpen
	}

	return agent.PlainReveal{
		Run: r.Run, Turn: r.Turn, CallID: r.CallID, Content: content, Values: values, At: r.At, ExpiresAt: r.ExpiresAt,
	}, nil
}

func revealAAD(run agent.RunID, turn int, call string) []byte {
	return fmt.Appendf(nil, "%s/%d/%s/", run, turn, call)
}

func gcm(key []byte) (cipher.AEAD, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("make a cipher: %w", err)
	}

	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("make GCM: %w", err)
	}

	return aead, nil
}

func seal(key, plain, aad []byte) ([]byte, error) {
	aead, err := gcm(key)
	if err != nil {
		return nil, err
	}

	nonce := make([]byte, aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, fmt.Errorf("make a nonce: %w", err)
	}

	return aead.Seal(nonce, nonce, plain, aad), nil
}

func open(key, sealed, aad []byte) ([]byte, error) {
	aead, err := gcm(key)
	if err != nil {
		return nil, err
	}

	if len(sealed) < aead.NonceSize() {
		return nil, agent.ErrRevealOpen
	}

	plain, err := aead.Open(nil, sealed[:aead.NonceSize()], sealed[aead.NonceSize():], aad)
	if err != nil {
		return nil, fmt.Errorf("open: %w", agent.ErrRevealOpen)
	}

	return plain, nil
}
