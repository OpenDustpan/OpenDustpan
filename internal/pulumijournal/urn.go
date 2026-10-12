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
// This file is derived from the following code of pulumi/pulumi at commit
// 128d3f2ec94e147915a6a928fdf7449d561d560e (tag v3.268.0):
//
//   - sdk/go/common/resource/urn/urn.go: Prefix, NameDelimiter, TypeDelimiter,
//     New, IsValid, Stack, Project, QualifiedType, Type and Name. The components
//     are read with strings.SplitN, which gives the same parts for a valid URN.
//   - sdk/go/common/tokens/tokens.go: the parts of Type that the URN and the
//     provider reference read (Module, Name and the delimiter count).
//
// OpenDustpan changed the types: a URN is a string, and each accessor returns an
// error where upstream asserts. Upstream panics on a malformed URN. The port
// never panics (ADR 43, decision 8.5).

package pulumijournal

import (
	"errors"
	"strings"
)

// Constants of the URN and token syntax (sdk/go/common/resource/urn/urn.go and
// sdk/go/common/tokens/names.go at commit 128d3f2).
const (
	urnPrefix        = "urn:pulumi:"
	urnNameDelimiter = "::"
	urnTypeDelimiter = "$"
	tokenDelimiter   = ":"
	// rootStackType is the type of the root component (tokens.RootStackType).
	rootStackType = "pulumi:pulumi:Stack"
)

// The positions of the components of a URN body, counted from the first name
// delimiter (urn.URN accessors, commit 128d3f2).
const (
	componentStack = iota
	componentProject
	componentQualifiedType
	componentName
)

// ErrInvalidURN is returned for a URN that does not have the URN syntax.
var ErrInvalidURN = errors.New("pulumijournal: invalid URN")

// isValidURN reports whether s has the URN prefix and at least three name
// delimiters (urn.URN.IsValid).
func isValidURN(s string) bool {
	return strings.HasPrefix(s, urnPrefix) && strings.Count(s, urnNameDelimiter) >= 3
}

// requireURN returns ErrInvalidURN when s is not a valid URN.
func requireURN(s string) error {
	if !isValidURN(s) {
		return ErrInvalidURN
	}
	return nil
}

// urnBody returns a valid URN without its prefix (urn.URN.URNName).
func urnBody(s string) string {
	return strings.TrimPrefix(s, urnPrefix)
}

// urnComponent returns component i of the body of a valid URN: 0 is the stack, 1
// the project and 2 the qualified type. A valid URN has at least three
// delimiters, so the split has the component. The split keeps the rest of the
// body in its last part, and the components read here are before it.
func urnComponent(s string, i int) string {
	// Two more parts than the index: the component, and the rest of the body.
	const restParts = 2
	return strings.SplitN(urnBody(s), urnNameDelimiter, i+restParts)[i]
}

// urnStack returns the stack of a valid URN (urn.URN.Stack).
func urnStack(s string) string {
	return urnComponent(s, componentStack)
}

// urnProject returns the project of a valid URN (urn.URN.Project).
func urnProject(s string) string {
	return urnComponent(s, componentProject)
}

// urnQualifiedType returns the qualified type of a valid URN (urn.URN.QualifiedType).
func urnQualifiedType(s string) string {
	return urnComponent(s, componentQualifiedType)
}

// urnType returns the type of a valid URN: the part of the qualified type after
// the last type delimiter (urn.URN.Type).
func urnType(s string) string {
	qualified := urnQualifiedType(s)
	last := strings.LastIndex(qualified, urnTypeDelimiter)
	return qualified[last+1:]
}

// urnName returns the name of a valid URN, which may hold name delimiters: the
// rest of the body after the third name delimiter (urn.URN.Name).
func urnName(s string) string {
	parts := strings.SplitN(urnBody(s), urnNameDelimiter, componentName+1)
	return parts[componentName]
}

// newURN builds a URN from its parts (urn.New). The qualified type is the parent
// type and the base type, joined by the type delimiter, unless the parent type
// is empty or the root stack type.
func newURN(stack, project, parentType, baseType, name string) string {
	typ := baseType
	if parentType != "" && parentType != rootStackType {
		typ = parentType + urnTypeDelimiter + typ
	}
	return urnPrefix + stack + urnNameDelimiter + project + urnNameDelimiter + typ + urnNameDelimiter + name
}

// typeModule returns the module of a type token (tokens.Type.Module). A type
// without a delimiter has no module. A type with one delimiter has no module
// member, and upstream panics on it. The port returns ErrInvalidURN for it.
func typeModule(typ string) (string, error) {
	if !strings.Contains(typ, tokenDelimiter) {
		return "", nil
	}
	end, err := moduleMemberDelimiter(typ)
	if err != nil {
		return "", err
	}
	return typ[:end], nil
}

// typeName returns the name of a type token (tokens.Type.Name). A type without
// a delimiter is its own name.
func typeName(typ string) (string, error) {
	if !strings.Contains(typ, tokenDelimiter) {
		return typ, nil
	}
	end, err := moduleMemberDelimiter(typ)
	if err != nil {
		return "", err
	}
	return typ[end+1:], nil
}

// moduleMemberDelimiter returns the index of the second token delimiter, which
// separates the module from the member. It returns ErrInvalidURN when the token
// has fewer than two delimiters.
func moduleMemberDelimiter(typ string) (int, error) {
	first := strings.Index(typ, tokenDelimiter)
	if first < 0 {
		return 0, ErrInvalidURN
	}
	second := strings.Index(typ[first+1:], tokenDelimiter)
	if second < 0 {
		return 0, ErrInvalidURN
	}
	return first + 1 + second, nil
}
