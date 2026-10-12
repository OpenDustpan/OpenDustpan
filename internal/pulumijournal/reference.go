// Copyright 2016, Pulumi Corporation.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.
//
// This file is derived from sdk/go/common/providers/reference.go of
// pulumi/pulumi at commit 128d3f2ec94e147915a6a928fdf7449d561d560e (tag
// v3.268.0). It ports these functions: UnknownID, validateURN, NewReference,
// ParseReference, Reference.URN, Reference.ID and Reference.String. The port
// returns an error where upstream asserts (contract.AssertNoErrorf), so it
// never panics.

package pulumijournal

import (
	"errors"
	"fmt"
	"strings"
)

// unknownProviderID is the ID that a provider reference gets when its ID is
// empty (providers.UnknownID).
const unknownProviderID = "04da6b54-80e4-46f7-96ec-b56ff0331ba9"

// ErrInvalidReference is returned for a provider reference that does not have
// the form <URN>::<ID>, or whose URN is not a provider URN.
var ErrInvalidReference = errors.New("pulumijournal: invalid provider reference")

// providerReference is a URN and an ID that name one provider instance
// (providers.Reference). Its string form is the URN, the name delimiter and the ID.
type providerReference struct {
	urn string
	id  string
}

// String returns the string form of the reference (Reference.String).
func (r providerReference) String() string {
	if r.urn == "" && r.id == "" {
		return ""
	}
	return r.urn + urnNameDelimiter + r.id
}

// validateProviderURN checks that urn is a provider URN: its type is in the
// module pulumi:providers and has a name (providers.validateURN).
func validateProviderURN(urn string) error {
	if !isValidURN(urn) {
		return fmt.Errorf("%w: %q is not a valid URN", ErrInvalidReference, urn)
	}
	typ := urnType(urn)
	module, err := typeModule(typ)
	if err != nil {
		return fmt.Errorf("%w: %q: %w", ErrInvalidReference, typ, err)
	}
	if module != "pulumi:providers" {
		return fmt.Errorf("%w: invalid module in type %q", ErrInvalidReference, typ)
	}
	name, err := typeName(typ)
	if err != nil {
		return fmt.Errorf("%w: %q: %w", ErrInvalidReference, typ, err)
	}
	if name == "" {
		return fmt.Errorf("%w: provider URN %q has no type name", ErrInvalidReference, urn)
	}
	return nil
}

// newProviderReference creates a reference for urn and id. An empty ID becomes
// the unknown ID, as providers.NewReference does (the ID is not kept empty).
func newProviderReference(urn, id string) (providerReference, error) {
	if err := validateProviderURN(urn); err != nil {
		return providerReference{}, err
	}
	if id == "" {
		id = unknownProviderID
	}
	return providerReference{urn: urn, id: id}, nil
}

// parseProviderReference parses the string form of a provider reference. It
// splits at the last name delimiter, so the URN may hold delimiters
// (providers.ParseReference).
func parseProviderReference(s string) (providerReference, error) {
	urn, id, found := strings.CutLast(s, urnNameDelimiter)
	if !found {
		return providerReference{}, fmt.Errorf("%w: expected %q in %q", ErrInvalidReference, urnNameDelimiter, s)
	}
	if err := validateProviderURN(urn); err != nil {
		return providerReference{}, err
	}
	return providerReference{urn: urn, id: id}, nil
}
