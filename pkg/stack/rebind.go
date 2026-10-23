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
// for a stack that does not exist. Only the entry secrets_providers of the
// deployment changes. Every other byte stays, and no secret is read.
//
// It returns deployment unchanged when the entry is missing, names another
// provider or has no object state, when the state already names ref, and when
// deployment does not decode: the caller serves what it holds then.
func RebindSecretsProvider(deployment []byte, ref Ref) []byte {
	span, ok := providersSpan(deployment)
	if !ok {
		return deployment
	}

	rebound, changed := reboundProviders(deployment[span.start:span.end], ref)
	if !changed {
		return deployment
	}

	out := make([]byte, 0, len(deployment)-(span.end-span.start)+len(rebound))
	out = append(out, deployment[:span.start]...)
	out = append(out, rebound...)
	out = append(out, deployment[span.end:]...)

	return out
}

// byteSpan is a range of bytes: start is the first byte, end the byte after
// the last.
type byteSpan struct {
	start, end int
}

// providersSpan finds the value of the member secrets_providers in the
// top-level object of deployment and returns its byte range. ok is false when
// deployment is not an object, does not decode, or has no such member.
func providersSpan(deployment []byte) (byteSpan, bool) {
	dec := jsontext.NewDecoder(bytes.NewReader(deployment))

	open, err := openStreamedObject(dec)
	if err != nil || !open {
		return byteSpan{start: 0, end: 0}, false
	}

	for {
		name, end, err := streamedMember(dec)
		if err != nil || end {
			return byteSpan{start: 0, end: 0}, false
		}

		if name != secretsProvidersMember {
			if skipErr := skipStreamed(dec); skipErr != nil {
				return byteSpan{start: 0, end: 0}, false
			}

			continue
		}

		raw, err := dec.ReadValue()
		if err != nil {
			return byteSpan{start: 0, end: 0}, false
		}

		stop := int(dec.InputOffset())

		return byteSpan{start: stop - len(raw), end: stop}, true
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
