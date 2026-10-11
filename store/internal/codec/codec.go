// Package codec converts between domain types and the column encodings shared
// by the SQL store adapters: timestamps as Unix milliseconds.
package codec

import (
	"time"

	"opendustpan.org/opendustpan/namespace"
	"opendustpan.org/opendustpan/secret"
	"opendustpan.org/opendustpan/stack"
)

// NamespaceRow is the column representation of a namespace.
type NamespaceRow struct {
	Name            string
	HomeRegion      string
	FailoverVersion int64
	CreatedAtMs     int64
}

// StackRow is the column representation of a stack. Tags live in the
// stack_tags table, keyed by the stack ID, so they are not columns here.
type StackRow struct {
	Namespace   string
	Project     string
	Name        string
	ID          string
	Version     int64
	CreatedAtMs int64
	UpdatedAtMs int64
}

// Millis encodes t as Unix milliseconds.
func Millis(t time.Time) int64 {
	return t.UnixMilli()
}

// FromMillis decodes Unix milliseconds as a UTC time.
func FromMillis(ms int64) time.Time {
	return time.UnixMilli(ms).UTC()
}

// FromStackRow converts a row and its tags into a domain stack. A nil tags
// map becomes an empty one, so callers always see an object and never null.
func FromStackRow(row StackRow, tags map[string]string) stack.Stack {
	if tags == nil {
		tags = map[string]string{}
	}

	return stack.Stack{
		CreatedAt: FromMillis(row.CreatedAtMs),
		UpdatedAt: FromMillis(row.UpdatedAtMs),
		Tags:      tags,
		Ref: stack.Ref{
			Namespace: namespace.Name(row.Namespace),
			Project:   stack.ProjectName(row.Project),
			Stack:     stack.Name(row.Name),
		},
		ID:      stack.ID(row.ID),
		Version: row.Version,
	}
}

// ToStackRow converts a domain stack into a row. Tags go to the stack_tags
// table, so they are not part of the row.
func ToStackRow(st stack.Stack) StackRow {
	return StackRow{
		Namespace:   st.Ref.Namespace.String(),
		Project:     st.Ref.Project.String(),
		Name:        st.Ref.Stack.String(),
		ID:          st.ID.String(),
		Version:     st.Version,
		CreatedAtMs: Millis(st.CreatedAt),
		UpdatedAtMs: Millis(st.UpdatedAt),
	}
}

// FromNamespaceRow converts a row into a domain namespace.
func FromNamespaceRow(row NamespaceRow) namespace.Namespace {
	return namespace.Namespace{
		CreatedAt:       FromMillis(row.CreatedAtMs),
		Name:            namespace.Name(row.Name),
		HomeRegion:      namespace.Region(row.HomeRegion),
		FailoverVersion: row.FailoverVersion,
	}
}

// ToNamespaceRow converts a domain namespace into a row.
func ToNamespaceRow(ns namespace.Namespace) NamespaceRow {
	return NamespaceRow{
		Name:            ns.Name.String(),
		HomeRegion:      ns.HomeRegion.String(),
		FailoverVersion: ns.FailoverVersion,
		CreatedAtMs:     Millis(ns.CreatedAt),
	}
}

// SecretKeyRow is the column representation of a wrapped data key, keyed by
// namespace and stack ID.
type SecretKeyRow struct {
	Namespace   string
	StackID     string
	WrapperID   string
	Ciphertext  []byte
	CreatedAtMs int64
}

// ToSecretKeyRow converts a domain wrapped key into a row.
func ToSecretKeyRow(k secret.WrappedKey) SecretKeyRow {
	return SecretKeyRow{
		Namespace:   k.Scope.Namespace.String(),
		StackID:     k.Scope.ID,
		WrapperID:   k.WrapperID,
		Ciphertext:  k.Ciphertext,
		CreatedAtMs: Millis(k.CreatedAt),
	}
}

// FromSecretKeyRow converts a row into a domain wrapped key.
func FromSecretKeyRow(row SecretKeyRow) secret.WrappedKey {
	return secret.WrappedKey{
		CreatedAt: FromMillis(row.CreatedAtMs),
		Scope: secret.Scope{
			Namespace: namespace.Name(row.Namespace),
			ID:        row.StackID,
		},
		WrapperID:  row.WrapperID,
		Ciphertext: row.Ciphertext,
	}
}
