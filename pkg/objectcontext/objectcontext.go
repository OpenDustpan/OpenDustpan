// Package objectcontext holds the context of every object: the summary, the
// runbook, the owners, the on-call and the links of a namespace, a
// collection, a unit, a resource of a unit or a blueprint.
//
// Context has two sources. A person or a client sets it through the API. A
// team writes it in the code of a unit, as the reserved output "opendustpan"
// with a schema version, or as the output "readme". The code goes through
// the review of the team, so for one field of one object the output wins
// over the API, and the API refuses to set a field that the output of the
// same object sets. Each field has one home.
//
// Context text comes from people. A reader, a person or an agent, treats it
// as data, never as instructions.
package objectcontext

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"opendustpan.org/opendustpan/internal/ident"
	"opendustpan.org/opendustpan/pkg/graph"
	"opendustpan.org/opendustpan/pkg/namespace"
)

// Sentinel errors of the context model and its ports.
var (
	// ErrInvalidObject reports an object reference that fails validation.
	ErrInvalidObject = errors.New("invalid context object")
	// ErrInvalidContext reports a field value out of its bounds. A
	// *RuleError wraps it and names the rule that the value breaks.
	ErrInvalidContext = errors.New("invalid context")
	// ErrFieldFromCode reports an API write of a field that the output of
	// the same object sets. The code is the home of that field. The error
	// names the field.
	ErrFieldFromCode = errors.New("the code of the unit sets this context field")
	// ErrNotFound reports an object with no stored context of the asked
	// source.
	ErrNotFound = errors.New("context not found")
	// ErrUnitDeleted reports an API write of the context of a unit that is
	// soft-deleted. A restore brings the unit back first.
	ErrUnitDeleted = errors.New("the unit is deleted")
	// ErrOutputInvalid reports a reserved output that is not a valid
	// context output.
	ErrOutputInvalid = errors.New("the opendustpan output is not valid")
	// ErrOutputVersion reports a reserved output with a schema version that
	// this server does not read.
	ErrOutputVersion = errors.New("the opendustpan output has an unknown schema version")
	// ErrUnknownSection reports a read API request for a section that does
	// not exist.
	ErrUnknownSection = errors.New("unknown context section")
)

// Bounds of context.
const (
	// MaxSummaryBytes bounds a summary.
	MaxSummaryBytes = 2 << 10
	// MaxRunbookBytes bounds a runbook and a readme.
	MaxRunbookBytes = 64 << 10
	// MaxOwners bounds the owners of one object.
	MaxOwners = 20
	// MaxOwnerBytes bounds one owner.
	MaxOwnerBytes = 200
	// MaxOnCallBytes bounds the on-call text.
	MaxOnCallBytes = 500
	// MaxLinks bounds the links of one object.
	MaxLinks = 50
	// MaxLinkTitleBytes bounds the title of a link.
	MaxLinkTitleBytes = 200
	// MaxURLBytes bounds the URL of a link.
	MaxURLBytes = 2 << 10
	// MaxEffectiveLinks bounds the links of a resolved view, from all layers.
	MaxEffectiveLinks = 100
	// MaxResourceKeyBytes bounds the key of a resource: a URN or an address.
	MaxResourceKeyBytes = 1 << 10
	// MaxBatch is the most objects that one call to the store names.
	MaxBatch = 500
)

// Limits of a name in a reference.
const maxNameLength = 100

// Kind is the kind of an object that carries context.
type Kind string

// The kinds of object.
const (
	KindNamespace  Kind = "namespace"
	KindCollection Kind = "collection"
	KindUnit       Kind = "unit"
	KindResource   Kind = "resource"
	KindBlueprint  Kind = "blueprint"
)

// Ref names one object. Namespace is set for each kind. Name is the name of
// a collection or a blueprint. Unit is set for a unit and a resource.
// Resource is the key of a resource in the state of its unit: the URN for the
// Pulumi CLI, the resource instance address for OpenTofu. The package keeps
// it opaque.
type Ref struct {
	Namespace namespace.Name
	Kind      Kind
	Name      string
	Resource  string
	Unit      graph.Unit
}

// NamespaceRef returns the reference of namespace ns.
func NamespaceRef(ns namespace.Name) Ref {
	return Ref{
		Kind:      KindNamespace,
		Namespace: ns,
		Name:      "",
		Resource:  "",
		Unit:      graph.Unit{Namespace: "", Tool: "", ID: ""},
	}
}

// CollectionRef returns the reference of the collection name in ns.
func CollectionRef(ns namespace.Name, name string) Ref {
	return Ref{
		Kind:      KindCollection,
		Namespace: ns,
		Name:      name,
		Resource:  "",
		Unit:      graph.Unit{Namespace: "", Tool: "", ID: ""},
	}
}

// BlueprintRef returns the reference of the blueprint name in ns.
func BlueprintRef(ns namespace.Name, name string) Ref {
	return Ref{
		Kind:      KindBlueprint,
		Namespace: ns,
		Name:      name,
		Resource:  "",
		Unit:      graph.Unit{Namespace: "", Tool: "", ID: ""},
	}
}

// UnitRef returns the reference of unit u.
func UnitRef(u graph.Unit) Ref {
	return Ref{Kind: KindUnit, Namespace: u.Namespace, Unit: u, Name: "", Resource: ""}
}

// ResourceRef returns the reference of the resource key of unit u.
func ResourceRef(u graph.Unit, key string) Ref {
	return Ref{Kind: KindResource, Namespace: u.Namespace, Unit: u, Resource: key, Name: ""}
}

// Validate reports whether r is a valid reference. It returns
// ErrInvalidObject otherwise.
func (r Ref) Validate() error {
	if _, err := namespace.ParseName(r.Namespace.String()); err != nil {
		return fmt.Errorf("%w: %w", ErrInvalidObject, err)
	}

	switch r.Kind {
	case KindNamespace:
		if r.Name != "" || r.Resource != "" || r.Unit != (graph.Unit{Namespace: "", Tool: "", ID: ""}) {
			return fmt.Errorf("%w: a namespace reference names only the namespace", ErrInvalidObject)
		}
	case KindCollection, KindBlueprint:
		if !ident.Valid(r.Name, maxNameLength) || r.Resource != "" ||
			r.Unit != (graph.Unit{Namespace: "", Tool: "", ID: ""}) {
			return fmt.Errorf("%w: %s %q", ErrInvalidObject, r.Kind, r.Name)
		}
	case KindUnit, KindResource:
		return r.validateUnit()
	default:
		return fmt.Errorf("%w: kind %q", ErrInvalidObject, r.Kind)
	}

	return nil
}

// Key returns a stable string for r: a store key and a page cursor.
func (r Ref) Key() string {
	switch r.Kind {
	case KindCollection, KindBlueprint:
		return string(r.Kind) + ":" + r.Namespace.String() + "/" + r.Name
	case KindUnit:
		return "unit:" + r.Unit.String()
	case KindResource:
		return "resource:" + r.Unit.String() + "#" + r.Resource
	case KindNamespace:
		return "namespace:" + r.Namespace.String()
	default:
		return string(r.Kind) + ":" + r.Namespace.String()
	}
}

func (r Ref) validateUnit() error {
	if err := r.Unit.Validate(); err != nil {
		return fmt.Errorf("%w: %w", ErrInvalidObject, err)
	}

	switch {
	case r.Unit.Namespace != r.Namespace:
		return fmt.Errorf("%w: the unit is in another namespace", ErrInvalidObject)
	case r.Name != "":
		return fmt.Errorf("%w: a unit reference has no name", ErrInvalidObject)
	case r.Kind == KindUnit && r.Resource != "":
		return fmt.Errorf("%w: a unit reference has no resource", ErrInvalidObject)
	case r.Kind == KindResource && (r.Resource == "" || !validText(r.Resource, MaxResourceKeyBytes)):
		return fmt.Errorf("%w: resource key", ErrInvalidObject)
	}

	return nil
}

// Source is where a context entry comes from.
type Source string

// The sources of context.
const (
	// SourceAPI is context that a person or a client set through the API.
	SourceAPI Source = "api"
	// SourceOutput is the reserved output "opendustpan" of a unit, from its
	// latest state item.
	SourceOutput Source = "output"
)

// Field names one field of context.
type Field string

// The fields of context.
const (
	FieldSummary Field = "summary"
	FieldRunbook Field = "runbook"
	FieldOwners  Field = "owners"
	FieldOnCall  Field = "onCall"
	FieldLinks   Field = "links"
)

// LinkKind says what a link points to. The console groups links by kind.
// The docs of an object are its runbook and its links of the kind docs.
type LinkKind string

// The kinds of link. An empty kind reads as LinkOther.
const (
	LinkDocs       LinkKind = "docs"
	LinkRunbook    LinkKind = "runbook"
	LinkDashboard  LinkKind = "dashboard"
	LinkRepository LinkKind = "repository"
	LinkOther      LinkKind = "other"
)

// Link is one link of an object. URL is an absolute http or https address.
type Link struct {
	Title string   `json:"title"`
	URL   string   `json:"url"`
	Kind  LinkKind `json:"kind,omitempty"`
}

// Fields is the context of one object from one source. An empty field is not
// set.
type Fields struct {
	Summary string `json:"summary,omitempty"`
	// Runbook is Markdown text: how to operate the object, and what is safe
	// to change.
	Runbook string `json:"runbook,omitempty"`
	// Owners are the teams or persons who own the object. See ParseOwner.
	Owners []string `json:"owners,omitempty"`
	// OnCall names where to page the people on call, for example a pager
	// service or a chat channel.
	OnCall string `json:"onCall,omitempty"`
	Links  []Link `json:"links,omitempty"`
}

// Set returns the fields that f sets, in a fixed order.
func (f Fields) Set() []Field {
	var out []Field

	for _, c := range []struct {
		field Field
		set   bool
	}{
		{FieldSummary, f.Summary != ""},
		{FieldRunbook, f.Runbook != ""},
		{FieldOwners, len(f.Owners) > 0},
		{FieldOnCall, f.OnCall != ""},
		{FieldLinks, len(f.Links) > 0},
	} {
		if c.set {
			out = append(out, c.field)
		}
	}

	return out
}

// RuleError reports a field value out of its bounds. Rule names the field
// and the rule that the value breaks, in words that a person can act on. It
// matches ErrInvalidContext with errors.Is.
type RuleError struct {
	Rule string
}

// Error implements error.
func (e *RuleError) Error() string {
	return ErrInvalidContext.Error() + ": " + e.Rule
}

// Is reports whether target is ErrInvalidContext.
func (e *RuleError) Is(target error) bool {
	return target == ErrInvalidContext
}

// breaks returns a *RuleError with the rule that the text of rule and args
// gives.
func breaks(rule string, args ...any) error {
	return &RuleError{Rule: fmt.Sprintf(rule, args...)}
}

// Validate reports whether each field of f is in its bounds. It returns a
// *RuleError, which matches ErrInvalidContext, otherwise.
func (f Fields) Validate() error {
	switch {
	case !validMultilineText(f.Summary, MaxSummaryBytes):
		return breaks("summary must be valid UTF-8 of %d bytes or fewer, "+
			"with no control character other than a line break or a tab", MaxSummaryBytes)
	case !validMultilineText(f.Runbook, MaxRunbookBytes):
		return breaks("runbook must be valid UTF-8 of %d bytes or fewer, "+
			"with no control character other than a line break or a tab", MaxRunbookBytes)
	case !validText(f.OnCall, MaxOnCallBytes):
		return breaks("onCall must be valid UTF-8 of %d bytes or fewer, with no control character", MaxOnCallBytes)
	case len(f.Owners) > MaxOwners:
		return breaks("owners must have %d entries or fewer", MaxOwners)
	case len(f.Links) > MaxLinks:
		return breaks("links must have %d entries or fewer", MaxLinks)
	}

	for _, o := range f.Owners {
		if _, err := ParseOwner(o); err != nil {
			return err
		}
	}

	for _, l := range f.Links {
		if err := l.Validate(); err != nil {
			return err
		}
	}

	return nil
}

// Validate reports whether l has a title, an http or https URL and a known
// kind. It returns a *RuleError, which matches ErrInvalidContext, otherwise.
func (l Link) Validate() error {
	switch {
	case strings.TrimSpace(l.Title) == "" || !validText(l.Title, MaxLinkTitleBytes):
		return breaks("each link needs a title of 1 to %d bytes, with no control character", MaxLinkTitleBytes)
	case len(l.URL) > MaxURLBytes || !webURL(l.URL):
		return breaks("each link needs an http or https URL of %d bytes or fewer", MaxURLBytes)
	}

	switch l.Kind {
	case "", LinkDocs, LinkRunbook, LinkDashboard, LinkRepository, LinkOther:
		return nil
	default:
		return breaks("link kind %q must be one of %s, %s, %s, %s and %s", l.Kind,
			LinkDocs, LinkRunbook, LinkDashboard, LinkRepository, LinkOther)
	}
}

// OwnerKind says what an owner names.
type OwnerKind string

// The kinds of owner.
const (
	// OwnerGroup is a group of OpenDustpan: "group:<name>".
	OwnerGroup OwnerKind = "group"
	// OwnerPrincipal is a principal of OpenDustpan: "principal:<ID>".
	OwnerPrincipal OwnerKind = "principal"
	// OwnerText is any other text, for example a team name that is not a
	// group. The server shows it and does not resolve it.
	OwnerText OwnerKind = "text"
)

// Owner is one parsed owner.
type Owner struct {
	Kind  OwnerKind
	Value string
}

// ParseOwner parses one owner. "group:<name>" names a group and
// "principal:<ID>" names a principal; the server resolves both, so the graph
// and a list by owner can use them. Other text is an owner of the kind text.
// It returns a *RuleError, which matches ErrInvalidContext, for an empty
// owner, an owner above MaxOwnerBytes or control characters, and for a group
// or principal with an invalid name.
func ParseOwner(s string) (Owner, error) {
	if strings.TrimSpace(s) == "" || !validText(s, MaxOwnerBytes) {
		return Owner{}, breaks("owner %q must be valid UTF-8 of 1 to %d bytes, with no control character",
			s, MaxOwnerBytes)
	}

	for _, k := range []OwnerKind{OwnerGroup, OwnerPrincipal} {
		if v, ok := strings.CutPrefix(s, string(k)+":"); ok {
			if !ident.Valid(v, maxNameLength) {
				return Owner{}, breaks("owner %q must name a %s with a valid name after %q", s, k, string(k)+":")
			}

			return Owner{Kind: k, Value: v}, nil
		}
	}

	return Owner{Kind: OwnerText, Value: s}, nil
}

// Entry is the stored context of one object from one source. Readme is the
// value of the output "readme" of a unit, for the source SourceOutput only.
// Item is the ID of the state item that the output came from, and is empty
// for the source SourceAPI. Warning is set when the output did not parse; the
// fields of the entry are then empty. UpdatedBy is the principal subject of
// an API write.
type Entry struct {
	UpdatedAt time.Time
	Object    Ref
	Source    Source
	Readme    string
	Item      string
	Warning   string
	UpdatedBy string
	Fields    Fields
}

// entryJSON is the wire form of Entry.
type entryJSON struct {
	UpdatedAt *time.Time `json:"updatedAt,omitempty"`
	Object    ObjectID   `json:"object"`
	Source    Source     `json:"source"`
	Readme    string     `json:"readme,omitempty"`
	Item      string     `json:"item,omitempty"`
	Warning   string     `json:"warning,omitempty"`
	UpdatedBy string     `json:"updatedBy,omitempty"`
	Fields    Fields     `json:"fields"`
}

// MarshalJSON implements json.Marshaler.
func (e Entry) MarshalJSON() ([]byte, error) {
	out := entryJSON{
		UpdatedAt: nil, Object: IDOf(e.Object), Source: e.Source, Readme: e.Readme, Item: e.Item,
		Warning: e.Warning, UpdatedBy: e.UpdatedBy, Fields: e.Fields,
	}

	if !e.UpdatedAt.IsZero() {
		at := e.UpdatedAt.UTC()
		out.UpdatedAt = &at
	}

	raw, err := json.Marshal(out)
	if err != nil {
		return nil, fmt.Errorf("encode entry: %w", err)
	}

	return raw, nil
}

// validText reports whether s is valid UTF-8 within maxBytes, with no
// control character.
func validText(s string, maxBytes int) bool {
	return validRunes(s, maxBytes, unicode.IsControl)
}

// validMultilineText reports whether s is valid UTF-8 within maxBytes, with
// no control character other than a line feed and a tab.
func validMultilineText(s string, maxBytes int) bool {
	return validRunes(s, maxBytes, func(r rune) bool {
		return unicode.IsControl(r) && r != '\n' && r != '\t'
	})
}

// validRunes reports whether s is valid UTF-8 within maxBytes, with no rune
// that refused reports.
func validRunes(s string, maxBytes int, refused func(rune) bool) bool {
	if len(s) > maxBytes || !utf8.ValidString(s) {
		return false
	}

	for _, r := range s {
		if refused(r) {
			return false
		}
	}

	return true
}

func webURL(s string) bool {
	u, err := url.Parse(s)
	if err != nil || u == nil {
		return false
	}

	return (u.Scheme == "https" || u.Scheme == "http") && u.Host != ""
}
