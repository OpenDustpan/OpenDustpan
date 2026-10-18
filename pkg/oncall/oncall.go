// Package oncall holds on-call schedules: who answers for a namespace or a
// team at a given time. A schedule is local, with layers of rotations and
// overrides, or a link to a schedule in an external on-call tool, read
// through the Provider port. The on-call field of the context of an object
// can name a schedule, and then resolves to the person on call now.
//
// The functions of this package are pure. The Store port keeps schedules,
// overrides and the copy of external shifts.
package oncall

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"opendustpan.org/opendustpan/pkg/calendar"
	"opendustpan.org/opendustpan/pkg/namespace"
)

// Sentinel errors returned by this package and by Store and Provider
// implementations.
var (
	// ErrInvalid reports a schedule, a layer or an override that fails
	// validation.
	ErrInvalid = errors.New("invalid on-call schedule")
	// ErrNotFound reports an unknown schedule.
	ErrNotFound = errors.New("on-call schedule not found")
	// ErrExists reports a create with an ID or a name that a schedule of
	// the namespace already has.
	ErrExists = errors.New("on-call schedule already exists")
	// ErrConflict reports a write that names an older version.
	ErrConflict = errors.New("on-call schedule changed since it was read")
	// ErrNobodyOnCall reports a time that no layer and no override covers.
	ErrNobodyOnCall = errors.New("nobody is on call")
	// ErrProviderUnavailable reports an external on-call tool that does
	// not answer, or that refuses the credentials of the installation.
	ErrProviderUnavailable = errors.New("on-call provider unavailable")
	// ErrUnknownExternal reports an external schedule key that the
	// provider does not know.
	ErrUnknownExternal = errors.New("external on-call schedule not found")
)

// Bounds of a schedule.
const (
	// IDPrefix starts every schedule ID.
	IDPrefix = "ocs-"
	// MaxNameLen caps the name of a schedule, in bytes. A name has the
	// form of a namespace name.
	MaxNameLen = 63
	// MaxLayers caps the layers of a schedule.
	MaxLayers = 5
	// MaxMembers caps the members of a layer.
	MaxMembers = 50
	// MaxOverrides caps the overrides of a schedule. AddOverride deletes
	// the overrides that ended before it counts, so the cap applies to
	// the overrides that did not end.
	MaxOverrides = 200
	// MinTurn is the shortest turn of a rotation.
	MinTurn = time.Hour
	// MaxRange caps the range of a timeline.
	MaxRange = 92 * 24 * time.Hour
	// SyncHorizon is how far ahead the server copies the shifts of an
	// external schedule.
	SyncHorizon = 14 * 24 * time.Hour
	// StaleAfter is the age after which the copy of an external schedule
	// shows as stale.
	StaleAfter = time.Hour
)

// SourceKind tells where the shifts of a schedule come from.
type SourceKind string

// Kinds of source.
const (
	// SourceLocal computes the shifts from the layers and the overrides.
	SourceLocal SourceKind = "local"
	// SourceExternal copies the shifts from an external on-call tool.
	SourceExternal SourceKind = "external"
)

// External names a schedule in an external on-call tool. Provider is the
// name of a configured provider adapter, and Key is the ID of the schedule
// in that tool.
type External struct {
	Provider string
	Key      string
}

// Hours limits a layer to some hours of some days, in the zone of the
// schedule, for example a layer for the working hours of a team. From is
// before To, and both are on one day.
type Hours struct {
	Days calendar.WeekdaySet
	From calendar.Clock
	To   calendar.Clock
}

// Layer is a rotation: Members take turns of length Turn, in order, from
// Start. Turns follow the wall clock of the zone of the schedule: turn k
// starts at the wall time of Start plus k turns, also over a daylight
// saving change. So a turn of 12 hours from 08:00 changes at 08:00 and
// 20:00 all year. A turn that starts in the hour that a change to summer
// time skips starts at the end of that hour. When Hours is set, the layer
// covers only those hours. A later layer of a schedule wins over an
// earlier one where both cover a time.
type Layer struct {
	Name    string
	Members []string
	Start   time.Time
	Turn    time.Duration
	Hours   *Hours
}

// Override is an on-call override: it puts Principal on call from Start
// to End, over every layer. CreatedBy is the principal that added it, at
// CreatedAt. The store sets CreatedAt from the time of its transaction.
// The overrides of a schedule are in the order of their adds, and the
// last one that covers a time wins.
type Override struct {
	ID        string
	Principal string
	Start     time.Time
	End       time.Time
	Reason    string
	CreatedBy string
	CreatedAt time.Time
}

// Schedule is an on-call schedule of a namespace. Name is unique in the
// namespace, so the on-call field of a context can name it. For a local
// schedule, Layers and Overrides give the shifts. For an external
// schedule, the store holds a copy of the shifts of the next SyncHorizon,
// and Synced is the time of that copy.
type Schedule struct {
	ID        string
	Namespace namespace.Name
	Name      string
	Zone      string
	Source    SourceKind
	External  External
	Layers    []Layer
	Overrides []Override
	Copied    []Shift
	Synced    time.Time
	Version   int64
}

// Shift is a time when one person is on call for a schedule. Principal is
// the principal ID, or empty for a person of an external tool that maps
// to no principal: Display then holds the name that the tool gives.
// Layer names the layer, or "override", or "external".
type Shift struct {
	Schedule  string
	Principal string
	Display   string
	Start     time.Time
	End       time.Time
	Layer     string
}

// Origin values of Shift.Layer that are not layer names.
const (
	OriginOverride = "override"
	OriginExternal = "external"
)

// Validate returns ErrInvalid for a schedule that breaks a bound.
func (s Schedule) Validate() error {
	if err := s.validateFields(); err != nil {
		return err
	}

	return s.validateParts()
}

// validateFields returns ErrInvalid for a field of the schedule that
// breaks a bound.
func (s Schedule) validateFields() error {
	switch {
	case !strings.HasPrefix(s.ID, IDPrefix):
		return fmt.Errorf("%w: a schedule ID starts with %q", ErrInvalid, IDPrefix)
	case !validNamespace(s.Namespace):
		return fmt.Errorf("%w: a schedule needs the name of a namespace", ErrInvalid)
	case !validName(s.Name):
		return fmt.Errorf(
			"%w: a schedule name has 1 to %d lowercase letters, digits and dashes",
			ErrInvalid,
			MaxNameLen,
		)
	case s.Source != SourceLocal && s.Source != SourceExternal:
		return fmt.Errorf("%w: unknown source %q", ErrInvalid, s.Source)
	case s.Source == SourceExternal && (s.External.Provider == "" || s.External.Key == ""):
		return fmt.Errorf("%w: an external schedule needs a provider and a key", ErrInvalid)
	case s.Source == SourceExternal && len(s.Layers) > 0:
		return fmt.Errorf("%w: an external schedule has no layers", ErrInvalid)
	default:
		return nil
	}
}

// validateParts returns ErrInvalid for a zone, a layer or an override of
// the schedule that breaks a bound.
func (s Schedule) validateParts() error {
	switch {
	case len(s.Layers) > MaxLayers:
		return fmt.Errorf("%w: at most %d layers", ErrInvalid, MaxLayers)
	case len(s.Overrides) > MaxOverrides:
		return fmt.Errorf("%w: at most %d overrides", ErrInvalid, MaxOverrides)
	default:
	}

	if !ValidZone(s.Zone) {
		return fmt.Errorf("%w: zone %q is not a name of the IANA time zone database", ErrInvalid, s.Zone)
	}

	for _, l := range s.Layers {
		if err := l.validate(); err != nil {
			return err
		}
	}

	for i := range s.Overrides {
		if err := s.Overrides[i].Validate(); err != nil {
			return err
		}
	}

	return nil
}

// Validate returns ErrInvalid for an override with no principal, no
// reason or no time of creation, or with an end that is not after its
// start.
func (o Override) Validate() error {
	if o.Principal == "" || o.Reason == "" || o.CreatedAt.IsZero() || !o.End.After(o.Start) {
		return fmt.Errorf(
			"%w: an override needs a principal, a reason, a time of creation, and an end after its start",
			ErrInvalid,
		)
	}

	return nil
}

func (l Layer) validate() error {
	switch {
	case l.Name == "" || l.Name == OriginOverride || l.Name == OriginExternal:
		return fmt.Errorf("%w: a layer needs a name other than %q and %q", ErrInvalid, OriginOverride, OriginExternal)
	case len(l.Members) == 0 || len(l.Members) > MaxMembers:
		return fmt.Errorf("%w: layer %s has 1 to %d members", ErrInvalid, l.Name, MaxMembers)
	case l.Start.IsZero():
		return fmt.Errorf("%w: layer %s needs a start", ErrInvalid, l.Name)
	case l.Turn < MinTurn:
		return fmt.Errorf("%w: layer %s has a turn of at least %s", ErrInvalid, l.Name, MinTurn)
	case l.Hours != nil && !l.Hours.valid():
		return fmt.Errorf("%w: layer %s has hours that end before they start", ErrInvalid, l.Name)
	case slices.Contains(l.Members, ""):
		return fmt.Errorf("%w: layer %s has an empty member", ErrInvalid, l.Name)
	default:
		return nil
	}
}

// valid reports whether h names at least one day, and hours that start
// before they end on one day.
func (h Hours) valid() bool {
	return !h.Days.Empty() && h.From >= 0 && h.From < h.To && h.To <= calendar.MinutesPerDay
}

// ValidZone reports whether zone is a name of the IANA time zone
// database, for example "Europe/Berlin" or "UTC", that the server can
// load with calendar.LoadZone. It refuses the names that follow the zone
// of the host, for example "Local", "localtime" and "/etc/localtime".
func ValidZone(zone string) bool {
	_, err := calendar.LoadZone(zone)

	return err == nil
}

// validNamespace reports whether ns is the name of a namespace. The
// server scope is not a namespace, so a schedule never belongs to it.
func validNamespace(ns namespace.Name) bool {
	_, err := namespace.ParseName(ns.String())

	return err == nil
}

func validName(n string) bool {
	if n == "" || len(n) > MaxNameLen || n[0] == '-' {
		return false
	}

	for _, r := range n {
		if (r < 'a' || r > 'z') && (r < '0' || r > '9') && r != '-' {
			return false
		}
	}

	return true
}
