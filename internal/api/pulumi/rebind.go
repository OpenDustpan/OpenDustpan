package pulumi

import (
	"bytes"
	"encoding/json"
	"encoding/json/jsontext"
	"fmt"

	"opendustpan.org/opendustpan/pkg/stack"
)

// secretsProvidersMember is the top-level member of a checkpoint that names
// the secrets provider of the stack.
const secretsProvidersMember = "secrets_providers"

// serviceSecretsProvider is the type of the secrets provider that the server
// holds for the stack.
const serviceSecretsProvider = "service"

// providerOwnerKey, providerProjectKey and providerStackKey are the members of
// the state of the service secrets provider that name the stack.
const (
	providerOwnerKey   = "owner"
	providerProjectKey = "project"
	providerStackKey   = "stack"
)

// rebindSecretsProvider returns deployment with the state of its service
// secrets provider rewritten to name ref: the owner, the project and the stack.
// The checkpoint keeps the name that the stack had when the CLI saved it. After
// a rename or a restore under a new name, the CLI would read that name and ask
// for a stack that does not exist. Only the top-level entries
// secrets_providers of the deployment change: each copy of a repeated entry
// is rebound. Every other byte stays, and no secret is read.
//
// It returns deployment unchanged when the entry is missing, names another
// provider or has no object state, when the state already names ref, and when
// deployment does not decode: the caller serves what it holds then.
func rebindSecretsProvider(deployment []byte, ref stack.Ref) []byte {
	spans, found := providersSpans(deployment)
	if !found {
		return deployment
	}

	var out []byte

	next := 0

	for _, span := range spans {
		rebound, changed := reboundProviders(deployment[span.start:span.end], ref)
		if !changed {
			continue
		}

		out = append(out, deployment[next:span.start]...)
		out = append(out, rebound...)
		next = span.end
	}

	if out == nil {
		return deployment
	}

	return append(out, deployment[next:]...)
}

// byteSpan is a range of bytes: start is the first byte, end the byte after
// the last.
type byteSpan struct {
	start, end int
}

// providersSpans finds the value of each top-level member secrets_providers
// of deployment, in order, and returns their byte ranges. The save of a
// checkpoint accepts a repeated member, so there can be more than one. ok is
// false when deployment is not an object, does not decode, or has no such
// member.
func providersSpans(deployment []byte) ([]byteSpan, bool) {
	dec := jsontext.NewDecoder(bytes.NewReader(deployment), jsontext.AllowDuplicateNames(true))

	if !openObject(dec) {
		return nil, false
	}

	var spans []byteSpan

	for {
		name, nameErr := dec.ReadToken()
		if nameErr != nil {
			return nil, false
		}

		if name.Kind() == jsontext.KindEndObject {
			return spans, len(spans) > 0
		}

		if name.Kind() != jsontext.KindString {
			return nil, false
		}

		if name.String() != secretsProvidersMember {
			if skipErr := dec.SkipValue(); skipErr != nil {
				return nil, false
			}

			continue
		}

		span, readErr := readSpan(dec)
		if readErr != nil {
			return nil, false
		}

		spans = append(spans, span)
	}
}

// openObject reads the first token of dec and reports whether it opens an
// object. It keeps the token check out of providersSpans, which the complexity
// limit of the linter bounds.
func openObject(dec *jsontext.Decoder) bool {
	first, err := dec.ReadToken()

	return err == nil && first.Kind() == jsontext.KindBeginObject
}

// readSpan reads the value that follows a member name and returns its byte
// range in the input of dec. It moves the span read out of providersSpans to
// keep that function within the complexity limit of the linter.
func readSpan(dec *jsontext.Decoder) (byteSpan, error) {
	raw, err := dec.ReadValue()
	if err != nil {
		return byteSpan{}, fmt.Errorf("read a value of %s: %w", secretsProvidersMember, err)
	}

	stop := int(dec.InputOffset())

	return byteSpan{start: stop - len(raw), end: stop}, nil
}

// reboundProviders returns the providers object raw with the stack members of
// its state set to ref. changed is false when raw needs no change.
func reboundProviders(raw []byte, ref stack.Ref) ([]byte, bool) {
	var providers map[string]json.RawMessage

	if err := json.Unmarshal(raw, &providers); err != nil {
		return nil, false
	}

	var kind string

	if err := json.Unmarshal(providers["type"], &kind); err != nil || kind != serviceSecretsProvider {
		return nil, false
	}

	var state map[string]json.RawMessage

	if err := json.Unmarshal(providers["state"], &state); err != nil || state == nil {
		return nil, false
	}

	want := map[string]string{
		providerOwnerKey:   ref.Namespace.String(),
		providerProjectKey: ref.Project.String(),
		providerStackKey:   ref.Stack.String(),
	}

	changed := false

	for key, value := range want {
		encoded, err := json.Marshal(value)
		if err != nil {
			return nil, false
		}

		if !bytes.Equal(state[key], encoded) {
			state[key] = encoded
			changed = true
		}
	}

	if !changed {
		return nil, false
	}

	encodedState, err := json.Marshal(state)
	if err != nil {
		return nil, false
	}

	providers["state"] = encodedState

	out, err := json.Marshal(providers)
	if err != nil {
		return nil, false
	}

	return out, true
}
