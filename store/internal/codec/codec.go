// Package codec converts between domain types and the column encodings shared
// by the SQL store adapters: timestamps as Unix milliseconds, tags as JSON.
package codec

import (
	"encoding/json"
	"fmt"
	"time"

	"opendustpan.org/opendustpan/namespace"
	"opendustpan.org/opendustpan/stack"
)

// NamespaceRow is the column representation of a namespace.
type NamespaceRow struct {
	Name            string
	HomeRegion      string
	FailoverVersion int64
	CreatedAtMs     int64
}

// StackRow is the column representation of a stack.
type StackRow struct {
	Namespace   string
	Project     string
	Name        string
	ID          string
	Tags        []byte
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

// EncodeTags encodes tags as a JSON object; nil encodes as {}.
func EncodeTags(tags map[string]string) ([]byte, error) {
	if tags == nil {
		tags = map[string]string{}
	}

	data, err := json.Marshal(tags)
	if err != nil {
		return nil, fmt.Errorf("encode tags: %w", err)
	}

	return data, nil
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

// FromStackRow converts a row into a domain stack.
func FromStackRow(row StackRow) (stack.Stack, error) {
	tags := map[string]string{}
	if err := json.Unmarshal(row.Tags, &tags); err != nil {
		return stack.Stack{}, fmt.Errorf("decode tags of %s/%s/%s: %w", row.Namespace, row.Project, row.Name, err)
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
	}, nil
}

// ToStackRow converts a domain stack into a row.
func ToStackRow(st stack.Stack) (StackRow, error) {
	tags, err := EncodeTags(st.Tags)
	if err != nil {
		return StackRow{}, err
	}

	return StackRow{
		Namespace:   st.Ref.Namespace.String(),
		Project:     st.Ref.Project.String(),
		Name:        st.Ref.Stack.String(),
		ID:          st.ID.String(),
		Tags:        tags,
		Version:     st.Version,
		CreatedAtMs: Millis(st.CreatedAt),
		UpdatedAtMs: Millis(st.UpdatedAt),
	}, nil
}

// FromStackRows converts rows into domain stacks.
func FromStackRows(rows []StackRow) ([]stack.Stack, error) {
	out := make([]stack.Stack, 0, len(rows))

	for _, row := range rows {
		st, err := FromStackRow(row)
		if err != nil {
			return nil, err
		}

		out = append(out, st)
	}

	return out, nil
}
