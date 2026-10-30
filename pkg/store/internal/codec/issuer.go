package codec

import (
	"encoding/json"
	"fmt"
	"slices"
	"time"

	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/profile"
)

// IssuerMillis encodes an optional time of the issuer tables: the zero time
// is 0.
func IssuerMillis(t time.Time) int64 {
	if t.IsZero() {
		return 0
	}

	return t.UnixMilli()
}

// FromIssuerMillis decodes IssuerMillis.
func FromIssuerMillis(ms int64) time.Time {
	if ms == 0 {
		return time.Time{}
	}

	return time.UnixMilli(ms).UTC()
}

// IssuerKeyRow holds the columns of one row of issuer_keys.
type IssuerKeyRow struct {
	ID          string
	State       string
	Public      []byte
	Sealed      []byte
	CreatedAt   int64
	ActivatedAt int64
	RetiredAt   int64
	DeletedAt   int64
}

// FromIssuerKeyRow converts a row of issuer_keys in namespace ns.
func FromIssuerKeyRow(ns namespace.Name, r IssuerKeyRow) profile.Key {
	return profile.Key{
		CreatedAt: FromIssuerMillis(r.CreatedAt), ActivatedAt: FromIssuerMillis(r.ActivatedAt),
		RetiredAt: FromIssuerMillis(r.RetiredAt), DeletedAt: FromIssuerMillis(r.DeletedAt),
		ID: profile.KeyID(r.ID), Namespace: ns, State: profile.KeyState(r.State),
		Public: slices.Clone(r.Public), Sealed: slices.Clone(r.Sealed),
	}
}

// ToIssuerKeyRow converts a key into the columns of issuer_keys.
func ToIssuerKeyRow(k profile.Key) IssuerKeyRow {
	return IssuerKeyRow{
		ID: string(k.ID), State: string(k.State), Public: k.Public, Sealed: k.Sealed,
		CreatedAt: IssuerMillis(k.CreatedAt), ActivatedAt: IssuerMillis(k.ActivatedAt),
		RetiredAt: IssuerMillis(k.RetiredAt), DeletedAt: IssuerMillis(k.DeletedAt),
	}
}

type copyCheckJSON struct {
	Steps    []copyStepJSON `json:"steps"`
	Verified []string       `json:"verified"`
	AtMs     int64          `json:"atMs"`
	OK       bool           `json:"ok"`
}

type copyStepJSON struct {
	Name   string `json:"name"`
	Detail string `json:"detail"`
	OK     bool   `json:"ok"`
}

// EncodeCopyCheck encodes a copy check for the column copy_check. A nil check
// is the empty text.
func EncodeCopyCheck(c *profile.CopyCheck) (string, error) {
	if c == nil {
		return "", nil
	}

	j := copyCheckJSON{Steps: make([]copyStepJSON, 0, len(c.Steps)), Verified: make([]string, 0, len(c.Verified)),
		AtMs: IssuerMillis(c.At), OK: c.OK}

	for _, s := range c.Steps {
		j.Steps = append(j.Steps, copyStepJSON{Name: s.Name, Detail: s.Detail, OK: s.OK})
	}

	for _, id := range c.Verified {
		j.Verified = append(j.Verified, string(id))
	}

	raw, err := json.Marshal(j)
	if err != nil {
		return "", fmt.Errorf("encode copy check: %w", err)
	}

	return string(raw), nil
}

// DecodeCopyCheck decodes EncodeCopyCheck. The empty text is a nil check.
func DecodeCopyCheck(raw string) (*profile.CopyCheck, error) {
	if raw == "" {
		return nil, nil //nolint:nilnil // No stored check is a normal state.
	}

	var j copyCheckJSON
	if err := json.Unmarshal([]byte(raw), &j); err != nil {
		return nil, fmt.Errorf("decode copy check: %w", err)
	}

	c := &profile.CopyCheck{At: FromIssuerMillis(j.AtMs), OK: j.OK, Steps: nil, Verified: nil}

	for _, s := range j.Steps {
		c.Steps = append(c.Steps, profile.CopyStep{Name: s.Name, Detail: s.Detail, OK: s.OK})
	}

	for _, id := range j.Verified {
		c.Verified = append(c.Verified, profile.KeyID(id))
	}

	return c, nil
}

// EncodeSubjectTemplate encodes a subject template for its column.
func EncodeSubjectTemplate(t []string) (string, error) {
	raw, err := json.Marshal(t)
	if err != nil {
		return "", fmt.Errorf("encode subject template: %w", err)
	}

	return string(raw), nil
}

// DecodeSubjectTemplate decodes EncodeSubjectTemplate and checks the result,
// so a row from another version cannot give a template that the rules refuse.
func DecodeSubjectTemplate(raw string) ([]string, error) {
	var t []string
	if err := json.Unmarshal([]byte(raw), &t); err != nil {
		return nil, fmt.Errorf("decode subject template: %w", err)
	}

	out, err := profile.CanonicalSubjectTemplate(t)
	if err != nil {
		return nil, fmt.Errorf("stored subject template: %w", err)
	}

	return out, nil
}
