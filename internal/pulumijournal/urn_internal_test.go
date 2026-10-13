package pulumijournal

import (
	"errors"
	"testing"
)

// TestURNParts checks the accessors of a valid URN, including a name with delimiters.
func TestURNParts(t *testing.T) {
	t.Parallel()
	const urn = "urn:pulumi:dev::app::pkg:mod:Parent$pkg:mod:Child::name::with::delimiters"
	if !isValidURN(urn) {
		t.Fatal("URN is not valid")
	}
	checks := []struct {
		name, got, want string
	}{
		{"stack", urnStack(urn), "dev"},
		{"project", urnProject(urn), "app"},
		{"qualified type", urnQualifiedType(urn), "pkg:mod:Parent$pkg:mod:Child"},
		{"type", urnType(urn), "pkg:mod:Child"},
		{"name", urnName(urn), "name::with::delimiters"},
	}
	for _, c := range checks {
		if c.got != c.want {
			t.Errorf("%s = %q, want %q", c.name, c.got, c.want)
		}
	}
}

// TestNewURN checks that the parent type is kept only when it is not empty and not the root stack.
func TestNewURN(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name, parent, base, want string
	}{
		{"no parent", "", "pkg:mod:T", "urn:pulumi:s::p::pkg:mod:T::n"},
		{"root stack", rootStackType, "pkg:mod:T", "urn:pulumi:s::p::pkg:mod:T::n"},
		{"parent type", "pkg:mod:P", "pkg:mod:T", "urn:pulumi:s::p::pkg:mod:P$pkg:mod:T::n"},
	}
	for _, tt := range tests {
		if got := newURN("s", "p", tt.parent, tt.base, "n"); got != tt.want {
			t.Errorf("%s: newURN = %q, want %q", tt.name, got, tt.want)
		}
	}
}

// TestTypeModuleAndName checks the module and the name of a type token. A token
// with one delimiter has no module member, so it is an error, not a panic.
func TestTypeModuleAndName(t *testing.T) {
	t.Parallel()
	tests := []struct {
		typ, module, name string
		err               error
	}{
		{"plain", "", "plain", nil},
		{"pulumi:providers:aws", "pulumi:providers", "aws", nil},
		{"pulumi:providers:aws:extra", "pulumi:providers", "aws:extra", nil},
		{"one:delimiter", "", "", ErrInvalidURN},
	}
	for _, tt := range tests {
		module, merr := typeModule(tt.typ)
		name, nerr := typeName(tt.typ)
		if tt.err != nil {
			if !errors.Is(merr, tt.err) || !errors.Is(nerr, tt.err) {
				t.Errorf("%s: errors %v and %v, want %v", tt.typ, merr, nerr, tt.err)
			}
			continue
		}
		if merr != nil || nerr != nil {
			t.Errorf("%s: unexpected errors %v and %v", tt.typ, merr, nerr)
		}
		if module != tt.module || name != tt.name {
			t.Errorf("%s: got module %q and name %q, want %q and %q", tt.typ, module, name, tt.module, tt.name)
		}
	}
}

// TestParseProviderReference checks the split at the last name delimiter and the
// validation of the provider URN.
func TestParseProviderReference(t *testing.T) {
	t.Parallel()
	const providerURN = "urn:pulumi:s::p::pulumi:providers:aws::default"
	tests := []struct {
		name, in, want string
		wantErr        bool
	}{
		{"with ID", providerURN + "::abc", providerURN + "::abc", false},
		{"empty ID", providerURN + "::", providerURN + "::", false},
		{"no delimiter", "abc", "", true},
		{"not a provider", "urn:pulumi:s::p::pkg:mod:T::n::abc", "", true},
		{"not a URN", "x::abc", "", true},
	}
	for _, tt := range tests {
		ref, err := parseProviderReference(tt.in)
		if (err != nil) != tt.wantErr {
			t.Errorf("%s: error %v, want error %t", tt.name, err, tt.wantErr)
			continue
		}
		if err == nil && ref.String() != tt.want {
			t.Errorf("%s: String = %q, want %q", tt.name, ref.String(), tt.want)
		}
	}
}

// TestNewProviderReferenceUnknownID checks that an empty ID becomes the unknown ID.
func TestNewProviderReferenceUnknownID(t *testing.T) {
	t.Parallel()
	ref, err := newProviderReference("urn:pulumi:s::p::pulumi:providers:aws::default", "")
	if err != nil {
		t.Fatal(err)
	}
	if ref.id != unknownProviderID {
		t.Fatalf("ID = %q, want %q", ref.id, unknownProviderID)
	}
}

// TestProviderReferenceStringEmpty checks that an empty reference has an empty string form.
func TestProviderReferenceStringEmpty(t *testing.T) {
	t.Parallel()
	if got := (providerReference{urn: "", id: ""}).String(); got != "" {
		t.Fatalf("String = %q, want empty", got)
	}
}
