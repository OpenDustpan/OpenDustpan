package profile

import (
	"context"
	"fmt"
	"net/url"
	"slices"
	"strings"
	"time"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/namespace"
)

// SubjectKeys lists the keys that a subject template can name, in the fixed
// order of the subject. A new key goes at the end.
func SubjectKeys() []string {
	return []string{
		"namespace", "namespace_id", "collection", "collection_id", "tool", "unit", "unit_id", "profile",
		"profile_id", "root_profile", "access", "phase", "action", "ref_type", "ref", "ref_protected",
		"runner_pool", "actor_type", "actor_id",
	}
}

// DefaultSubjectTemplate is the template of a namespace that sets none.
func DefaultSubjectTemplate() []string {
	return []string{"namespace", "collection", "tool", "unit", "access"}
}

// CopyStep is the outcome of one step of the copy check.
type CopyStep struct {
	Name   string
	Detail string
	OK     bool
}

// CopyCheck is the outcome of the last copy check of a namespace with an
// issuerURL. Verified holds each key ID that a check found in the copied
// JWKS and that the namespace still has: the server signs only with such a
// key.
type CopyCheck struct {
	At       time.Time
	Steps    []CopyStep
	Verified []KeyID
	OK       bool
}

// Holds reports whether a check found the key id in the copy.
func (c *CopyCheck) Holds(id KeyID) bool {
	return c != nil && slices.Contains(c.Verified, id)
}

// IssuerSettings are the settings of the issuer of one namespace. Version 0
// means that the namespace stored none: the fields hold the defaults.
// SubjectTemplate is always set, in the order of SubjectKeys.
type IssuerSettings struct {
	UpdatedAt          time.Time
	CopyCheck          *CopyCheck
	Namespace          namespace.Name
	IssuerURL          string
	UpdatedBy          string
	SubjectTemplate    []string
	Version            int64
	AllowBlockSubjects bool
	IncludeActorName   bool
}

// DefaultIssuerSettings returns the settings of a namespace that stored none.
func DefaultIssuerSettings(ns namespace.Name) IssuerSettings {
	return IssuerSettings{
		UpdatedAt: time.Time{}, CopyCheck: nil, Namespace: ns, IssuerURL: "", UpdatedBy: "",
		SubjectTemplate: DefaultSubjectTemplate(), Version: 0, AllowBlockSubjects: true, IncludeActorName: false,
	}
}

// ETag returns the entity tag of the settings.
func (s IssuerSettings) ETag() string {
	return `"issuer.` + itoa(int(s.Version)) + `"`
}

// SettingsPatch is a JSON Merge Patch of the settings: a nil field keeps the
// value. An empty IssuerURL clears the URL. An empty SubjectTemplate
// restores the default.
type SettingsPatch struct {
	IssuerURL          *string
	SubjectTemplate    *[]string
	AllowBlockSubjects *bool
	IncludeActorName   *bool
}

// Apply returns s with the patch, checked. ErrInvalid for a value that fails
// the rules.
func (p SettingsPatch) Apply(s IssuerSettings) (IssuerSettings, error) {
	out := s
	out.SubjectTemplate = slices.Clone(s.SubjectTemplate)

	if p.IssuerURL != nil {
		if err := CheckIssuerURL(*p.IssuerURL); err != nil {
			return IssuerSettings{}, err
		}

		out.IssuerURL = *p.IssuerURL
	}

	if p.SubjectTemplate != nil {
		t, err := CanonicalSubjectTemplate(*p.SubjectTemplate)
		if err != nil {
			return IssuerSettings{}, err
		}

		out.SubjectTemplate = t
	}

	if p.AllowBlockSubjects != nil {
		out.AllowBlockSubjects = *p.AllowBlockSubjects
	}

	if p.IncludeActorName != nil {
		out.IncludeActorName = *p.IncludeActorName
	}

	return out, nil
}

// CanonicalSubjectTemplate checks a template and returns it in the order of
// SubjectKeys. An empty template is the default. It refuses an unknown key,
// a repeated key and a template with neither namespace nor namespace_id.
func CanonicalSubjectTemplate(t []string) ([]string, error) {
	if len(t) == 0 {
		return DefaultSubjectTemplate(), nil
	}

	order := SubjectKeys()
	seen := map[string]bool{}

	for _, k := range t {
		if !slices.Contains(order, k) {
			return nil, fmt.Errorf("%w: the subject template names the key %q, which is not a subject key", ErrInvalid, k)
		}

		if seen[k] {
			return nil, fmt.Errorf("%w: the subject template names the key %q twice", ErrInvalid, k)
		}

		seen[k] = true
	}

	if !seen["namespace"] && !seen["namespace_id"] {
		return nil, fmt.Errorf("%w: the subject template must name namespace or namespace_id", ErrInvalid)
	}

	out := make([]string, 0, len(t))

	for _, k := range order {
		if seen[k] {
			out = append(out, k)
		}
	}

	return out, nil
}

// CheckIssuerURL checks an issuerURL: empty, or an absolute https URL with a
// host, no user information, no query, no fragment and no trailing slash.
// The server refuses a value and does not change it.
func CheckIssuerURL(raw string) error {
	if raw == "" {
		return nil
	}

	bad := func(why string) error {
		return fmt.Errorf("%w: issuerURL %s: %s", ErrInvalid, quoteShort(raw), why)
	}

	if len(raw) > MaxIssuerURLBytes {
		return bad("it is longer than 2048 bytes")
	}

	for _, r := range raw {
		if r < 0x21 || r == 0x7f {
			return bad("it holds a space or a control character")
		}
	}

	u, err := url.Parse(raw)
	if err != nil || !u.IsAbs() || u.Scheme != "https" || u.Hostname() == "" {
		return bad("use an absolute https URL with a host")
	}

	switch {
	case u.User != nil:
		return bad("it holds user information")
	case u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || strings.Contains(raw, "#"):
		return bad("it holds a query or a fragment")
	case strings.HasSuffix(raw, "/"):
		return bad("remove the trailing slash")
	}

	return nil
}

func quoteShort(s string) string {
	if len(s) > 60 {
		s = s[:60] + "..."
	}

	return fmt.Sprintf("%q", s)
}

// NeedsFreshStep reports whether the change from old to next reduces
// security, so that it needs a fresh MFA step: a change of issuerURL, a
// change of the subject template, allowBlockSubjects from false to true and
// includeActorName from false to true.
func NeedsFreshStep(old, next IssuerSettings) bool {
	return old.IssuerURL != next.IssuerURL ||
		!slices.Equal(old.SubjectTemplate, next.SubjectTemplate) ||
		(!old.AllowBlockSubjects && next.AllowBlockSubjects) ||
		(!old.IncludeActorName && next.IncludeActorName)
}

// IssuerSettingsStore persists the settings of the issuer of each namespace.
// A write takes the fence and the audit entries, as Keys.Apply does.
type IssuerSettingsStore interface {
	// GetIssuerSettings reads the settings, or the defaults with Version 0
	// for a namespace that stored none. namespace.ErrNotFound for an unknown
	// namespace.
	GetIssuerSettings(ctx context.Context, ns namespace.Name) (IssuerSettings, error)
	// PutIssuerSettings stores s when the stored Version is ifVersion, and
	// returns the settings with the next Version. It keeps the stored copy
	// check. ErrConflict for a stale version, ErrInvalid for settings that
	// fail the rules.
	PutIssuerSettings(
		ctx context.Context, s IssuerSettings, ifVersion int64, f namespace.Fence, e []audit.Entry,
	) (IssuerSettings, error)
	// RecordCopyCheck stores the outcome of a copy check. It does not change
	// Version. Verified keeps only IDs that the check lists.
	RecordCopyCheck(ctx context.Context, ns namespace.Name, c CopyCheck, f namespace.Fence, e []audit.Entry) error
}
