package gate

import (
	"fmt"
	"strconv"
	"strings"
	"unicode/utf8"

	"opendustpan.org/opendustpan/internal/ident"
	"opendustpan.org/opendustpan/pkg/collection"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/namespace"
)

// Level is the kind of a scope.
type Level string

// The levels of a scope. Installation, namespace, collection and unit form
// the chain of the configuration routes, widest first. A blueprint version
// and a profile hold gates that are part of the object itself.
const (
	LevelInstallation Level = "installation"
	LevelNamespace    Level = "namespace"
	LevelCollection   Level = "collection"
	LevelUnit         Level = "unit"
	LevelBlueprint    Level = "blueprint"
	LevelProfile      Level = "profile"
)

// segments returns the count of variable segments in the scope key of l,
// and false for an unknown level.
func (l Level) segments() (int, bool) {
	switch l {
	case LevelInstallation:
		return 0, true
	case LevelNamespace:
		return 1, true
	case LevelCollection, LevelBlueprint, LevelProfile:
		return 2, true //nolint:mnd // A collection, a blueprint version and a profile each have two segments.
	case LevelUnit:
		return 3, true //nolint:mnd // A unit has a namespace, a tool and a name.
	default:
		return 0, false
	}
}

// maxSegmentLength bounds a profile name in a scope key, before encoding.
const maxSegmentLength = 300

// MaxBlueprintNameBytes is the most bytes in the name of a blueprint. It
// equals the bound of the catalog, which a test of the catalog checks.
const MaxBlueprintNameBytes = 63

// MaxScopeKeyLength is the length in bytes of the longest scope key: a unit
// scope with a namespace of 100 bytes, a tool of 100 bytes and a name of 300
// bytes that each encode to three bytes. ParseScopeKey refuses a longer key.
const MaxScopeKeyLength = len("unit/") + 100 + len("/") + 100 + len("/") + 3*300

// Scope is where a gate, an access limit, an approval rule, an ownership
// rule, a path set or a setting applies. Its scope key is one of:
//
//	installation
//	namespace/<namespace>
//	collection/<namespace>/<collection>
//	unit/<namespace>/<tool>/<name>
//	blueprint/<blueprint>/<version number>
//	profile/<namespace>/<profile>
//
// Each variable segment is percent-encoded, so a segment never holds '/'.
// The Pulumi stack web/prod of acme has the key unit/acme/pulumi/web%2Fprod.
// The key is a one-way door: stored policy IDs, records and requirement
// names hold it.
//
// The key of a unit scope names the unit by its name, as the routes do. The
// configuration of a unit keys on the stable ID of the unit: a store finds the
// live unit that has the name at the time of the read or the write. So the
// configuration stays with the unit through a rename and a move between
// collections, and a new unit with the name of a deleted unit has none.
type Scope struct {
	level Level
	// seg holds the decoded variable segments, in key order.
	seg [3]string
}

// InstallationScope returns the scope of the installation.
func InstallationScope() Scope {
	return Scope{level: LevelInstallation, seg: [3]string{}}
}

// NamespaceScope returns the scope of the namespace ns.
func NamespaceScope(ns namespace.Name) Scope {
	return Scope{level: LevelNamespace, seg: [3]string{ns.String(), "", ""}}
}

// CollectionScope returns the scope of the collection c of ns. Collection
// names compare without case, so the scope holds the key of the name: two
// spellings of one collection give one scope.
func CollectionScope(ns namespace.Name, c collection.Name) Scope {
	return Scope{level: LevelCollection, seg: [3]string{ns.String(), c.Key(), ""}}
}

// UnitScope returns the scope of one unit: the tool code, the namespace ns
// and the name that the tool gives the unit. The tool and the name follow
// the rules of a unit reference. The name is opaque and keeps its case. The
// scope reaches the configuration of the live unit that has the name.
func UnitScope(ns namespace.Name, tool, name string) (Scope, error) {
	if _, err := identity.UnitObject(tool, ns, name); err != nil {
		return Scope{}, fmt.Errorf("%w: unit scope: %w", ErrInvalid, err)
	}

	return Scope{level: LevelUnit, seg: [3]string{ns.String(), tool, name}}, nil
}

// BlueprintScope returns the scope of one blueprint version: the blueprint
// name, which is 1 to MaxBlueprintNameBytes bytes of lowercase letters, digits
// and '-' with a letter first, and the version number, from 1.
// The key follows the current decision and holds no namespace.
func BlueprintScope(name string, version int) (Scope, error) {
	if !ident.ValidSlug(name, MaxBlueprintNameBytes) || version < 1 {
		return Scope{}, fmt.Errorf("%w: blueprint scope %q version %d", ErrInvalid, name, version)
	}

	return Scope{level: LevelBlueprint, seg: [3]string{name, strconv.Itoa(version), ""}}, nil
}

// ProfileScope returns the scope of the profile name of ns. No owner package
// of profiles holds a name grammar yet, so the name is 1 to 300 bytes of
// valid UTF-8.
func ProfileScope(ns namespace.Name, name string) (Scope, error) {
	if !validSegment(name) {
		return Scope{}, fmt.Errorf("%w: profile scope %q", ErrInvalid, name)
	}

	return Scope{level: LevelProfile, seg: [3]string{ns.String(), name, ""}}, nil
}

// validSegment reports whether s can be a profile segment: 1 to 300 bytes of
// valid UTF-8.
func validSegment(s string) bool {
	return s != "" && len(s) <= maxSegmentLength && utf8.ValidString(s)
}

// Level returns the level of s. The zero Scope has the empty level.
func (s Scope) Level() Level {
	return s.level
}

// IsZero reports whether s is the zero Scope, which names no scope.
func (s Scope) IsZero() bool {
	return s.level == ""
}

// Namespace returns the namespace of s, and the empty name for the
// installation and a blueprint version.
func (s Scope) Namespace() namespace.Name {
	if s.level == LevelInstallation || s.level == LevelBlueprint {
		return ""
	}

	return namespace.Name(s.seg[0])
}

// Collection returns the key of the collection name of a collection scope,
// and the empty string for any other level.
func (s Scope) Collection() string {
	if s.level != LevelCollection {
		return ""
	}

	return s.seg[1]
}

// Unit returns the reference of the unit of a unit scope, and false for
// any other level.
func (s Scope) Unit() (identity.Object, bool) {
	if s.level != LevelUnit {
		return identity.Object{}, false
	}

	obj, err := identity.UnitObject(s.seg[1], namespace.Name(s.seg[0]), s.seg[2])
	if err != nil {
		return identity.Object{}, false
	}

	return obj, true
}

// BlueprintVersion names one version of one blueprint.
type BlueprintVersion struct {
	Name    string
	Version int
}

// Blueprint returns the blueprint version of a blueprint scope, and false
// for any other level.
func (s Scope) Blueprint() (BlueprintVersion, bool) {
	if s.level != LevelBlueprint {
		return BlueprintVersion{Name: "", Version: 0}, false
	}

	v, err := strconv.Atoi(s.seg[1])
	if err != nil {
		return BlueprintVersion{Name: "", Version: 0}, false
	}

	return BlueprintVersion{Name: s.seg[0], Version: v}, true
}

// Profile returns the profile name of a profile scope, and false for any
// other level.
func (s Scope) Profile() (string, bool) {
	if s.level != LevelProfile {
		return "", false
	}

	return s.seg[1], true
}

// HoldsConfig reports whether s has the configuration routes: the
// installation, a namespace, a collection or a unit. A gate of a blueprint
// version or of a profile is part of that object and has no route.
func (s Scope) HoldsConfig() bool {
	return s.level == LevelInstallation || s.level == LevelNamespace ||
		s.level == LevelCollection || s.level == LevelUnit
}

// HoldsApprovalDefault reports whether s can hold an approval default: a
// namespace, a collection or a unit. The installation holds none.
func (s Scope) HoldsApprovalDefault() bool {
	return s.HoldsConfig() && s.level != LevelInstallation
}

// Key returns the scope key of s, and the empty string for the zero Scope.
func (s Scope) Key() string {
	n, ok := s.level.segments()
	if !ok {
		return ""
	}

	var b strings.Builder

	b.WriteString(string(s.level))

	for i := range n {
		b.WriteByte('/')
		b.WriteString(escapeSegment(s.seg[i]))
	}

	return b.String()
}

// String returns the scope key.
func (s Scope) String() string {
	return s.Key()
}

// MarshalText returns the scope key. The zero Scope gives the empty text.
func (s Scope) MarshalText() ([]byte, error) {
	return []byte(s.Key()), nil
}

// UnmarshalText parses text with ParseScopeKey. The empty text gives the
// zero Scope.
func (s *Scope) UnmarshalText(text []byte) error {
	if len(text) == 0 {
		*s = Scope{level: "", seg: [3]string{}}

		return nil
	}

	scope, err := ParseScopeKey(string(text))
	if err != nil {
		return err
	}

	*s = scope

	return nil
}

// ParseScopeKey parses s as a scope key. It returns ErrInvalid for a key
// longer than MaxScopeKeyLength, an unknown level, a wrong count of
// segments, a segment that is not in the canonical percent-encoding, and a
// segment that fails the rules of its object.
func ParseScopeKey(s string) (Scope, error) {
	if len(s) > MaxScopeKeyLength {
		return Scope{}, fmt.Errorf("%w: scope key of %d bytes, the most is %d", ErrInvalid, len(s), MaxScopeKeyLength)
	}

	scope, rest, err := cutScopeKey(s)
	if err != nil {
		return Scope{}, err
	}

	if rest != "" {
		return Scope{}, fmt.Errorf("%w: scope key %q has extra segments", ErrInvalid, s)
	}

	return scope, nil
}

// cutScopeKey parses the scope key at the start of s and returns the rest
// of s, which is empty or starts with '/'.
func cutScopeKey(s string) (Scope, string, error) {
	parts := strings.Split(s, "/")
	level := Level(parts[0])

	n, ok := level.segments()
	if !ok {
		return Scope{}, "", fmt.Errorf("%w: scope key %q: unknown level %q", ErrInvalid, s, parts[0])
	}

	if len(parts) < 1+n {
		return Scope{}, "", fmt.Errorf("%w: scope key %q: a %s scope has %d segments", ErrInvalid, s, level, n)
	}

	var seg [3]string

	for i := range n {
		dec, err := unescapeSegment(parts[1+i])
		if err != nil {
			return Scope{}, "", fmt.Errorf("scope key %q: %w", s, err)
		}

		seg[i] = dec
	}

	scope, err := buildScope(level, seg)
	if err != nil {
		return Scope{}, "", fmt.Errorf("scope key %q: %w", s, err)
	}

	rest := ""
	if len(parts) > 1+n {
		rest = "/" + strings.Join(parts[1+n:], "/")
	}

	return scope, rest, nil
}

// buildScope validates the decoded segments of level through the
// constructor of that level, and refuses a segment that the constructor
// would store in another form.
func buildScope(level Level, seg [3]string) (Scope, error) {
	if level == LevelInstallation {
		return InstallationScope(), nil
	}

	if level == LevelBlueprint {
		return parseBlueprintScope(seg[0], seg[1])
	}

	ns, err := namespace.ParseName(seg[0])
	if err != nil {
		return Scope{}, fmt.Errorf("%w: %w", ErrInvalid, err)
	}

	if level == LevelNamespace {
		return NamespaceScope(ns), nil
	}

	if level == LevelUnit {
		return UnitScope(ns, seg[1], seg[2])
	}

	if level == LevelProfile {
		return ProfileScope(ns, seg[1])
	}

	c, err := collection.ParseName(seg[1])
	if err != nil || c.Key() != seg[1] {
		return Scope{}, fmt.Errorf("%w: collection %q is not a lowercase collection name", ErrInvalid, seg[1])
	}

	return CollectionScope(ns, c), nil
}

// parseBlueprintScope parses the segments of a blueprint scope key. The
// version is a decimal number from 1, with no sign and no leading zero.
func parseBlueprintScope(name, version string) (Scope, error) {
	n, ok := parseVersionNumber(version)
	if !ok || strconv.Itoa(n) != version {
		return Scope{}, fmt.Errorf("%w: blueprint version %q is not a number from 1", ErrInvalid, version)
	}

	return BlueprintScope(name, n)
}

// UnitChain returns the scopes whose configuration holds for unit, widest
// first: the installation, the namespace of the unit, the collection of the
// unit when it has one, and the unit. Give the empty collection name for a
// unit in no collection. It returns ErrInvalid for an object that is not a
// unit.
func UnitChain(unit identity.Object, c collection.Name) ([]Scope, error) {
	if !unit.IsUnit() {
		return nil, fmt.Errorf("%w: %q is not a unit", ErrInvalid, unit.String())
	}

	u, err := UnitScope(unit.Namespace(), unit.Tool(), unit.UnitName())
	if err != nil {
		return nil, err
	}

	chain := []Scope{InstallationScope(), NamespaceScope(unit.Namespace())}
	if c != "" {
		chain = append(chain, CollectionScope(unit.Namespace(), c))
	}

	return append(chain, u), nil
}

// unreserved reports whether c is an unreserved character of RFC 3986,
// which a segment holds as is.
func unreserved(c byte) bool {
	switch {
	case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9':
		return true
	default:
		return c == '-' || c == '.' || c == '_' || c == '~'
	}
}

const upperHex = "0123456789ABCDEF"

// hexDigitBits is the count of bits of one hex digit.
const hexDigitBits = 4

// escapeSegment percent-encodes each byte of s that is not unreserved, with
// uppercase hex digits. The result holds no '/'.
func escapeSegment(s string) string {
	var b strings.Builder

	for _, c := range []byte(s) {
		if unreserved(c) {
			b.WriteByte(c)

			continue
		}

		b.WriteByte('%')
		b.WriteByte(upperHex[c>>hexDigitBits])
		b.WriteByte(upperHex[c&0x0f])
	}

	return b.String()
}

// unescapeSegment decodes s, and refuses each form that escapeSegment
// never gives: a raw byte that is not unreserved, a lowercase hex digit,
// and an encoded unreserved byte. So each scope has exactly one key.
func unescapeSegment(s string) (string, error) {
	var b strings.Builder

	for i := 0; i < len(s); i++ {
		c := s[i]

		if unreserved(c) {
			b.WriteByte(c)

			continue
		}

		if c != '%' || i+2 >= len(s) {
			return "", fmt.Errorf("%w: segment %q is not percent-encoded", ErrInvalid, s)
		}

		hi, okHi := hexValue(s[i+1])
		lo, okLo := hexValue(s[i+2])

		if !okHi || !okLo {
			return "", fmt.Errorf("%w: segment %q: use two uppercase hex digits after %%", ErrInvalid, s)
		}

		dec := hi<<hexDigitBits | lo
		if unreserved(dec) {
			return "", fmt.Errorf("%w: segment %q encodes the unreserved character %q", ErrInvalid, s, dec)
		}

		b.WriteByte(dec)

		i += 2
	}

	return b.String(), nil
}

// hexValue returns the value of the uppercase hex digit c, and false for
// any other byte.
func hexValue(c byte) (byte, bool) {
	switch {
	case c >= '0' && c <= '9':
		return c - '0', true
	case c >= 'A' && c <= 'F':
		return c - 'A' + 10, true //nolint:mnd // The hex digit A has the value 10.
	default:
		return 0, false
	}
}
