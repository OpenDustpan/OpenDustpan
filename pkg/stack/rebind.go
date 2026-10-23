package stack

import (
	"bytes"
	"encoding/json"
	"encoding/json/jsontext"
)

// providerOwnerKey, providerProjectKey and providerStackKey are the members of
// the state of the service secrets provider that name the stack.
const (
	providerOwnerKey   = "owner"
	providerProjectKey = "project"
	providerStackKey   = "stack"
)

// RebindSecretsProvider returns deployment with the state of its service
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
func RebindSecretsProvider(deployment []byte, ref Ref) []byte {
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

	open, err := openStreamedObject(dec)
	if err != nil || !open {
		return nil, false
	}

	var spans []byteSpan

	for {
		name, end, err := streamedMember(dec)
		if err != nil {
			return nil, false
		}

		if end {
			return spans, len(spans) > 0
		}

		if name != secretsProvidersMember {
			if skipErr := skipStreamed(dec); skipErr != nil {
				return nil, false
			}

			continue
		}

		raw, err := dec.ReadValue()
		if err != nil {
			return nil, false
		}

		stop := int(dec.InputOffset())
		spans = append(spans, byteSpan{start: stop - len(raw), end: stop})
	}
}

// reboundProviders returns the providers object raw with the stack members of
// its state set to ref. changed is false when raw needs no change.
func reboundProviders(raw []byte, ref Ref) ([]byte, bool) {
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
