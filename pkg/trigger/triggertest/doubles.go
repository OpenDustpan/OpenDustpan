package triggertest

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/graph"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/trigger"
)

// Doubles are the in-memory ports around a Dispatcher: the saver check, the
// units, the audit log of the refusals and the state versions. An adapter test
// builds its Dispatcher with Doubles.Dispatcher over its own Store, and the
// suite drives the doubles through the methods below.
type Doubles struct {
	revoked map[string]bool
	reused  map[string]string
	audited map[namespace.Name][]audit.Entry
	last    map[graph.Unit]string
	next    int
	mu      sync.Mutex
}

// NewDoubles returns doubles that allow every saver and know no state version.
func NewDoubles() *Doubles {
	return &Doubles{
		revoked: map[string]bool{},
		reused:  map[string]string{},
		audited: map[namespace.Name][]audit.Entry{},
		last:    map[graph.Unit]string{},
		next:    0,
		mu:      sync.Mutex{},
	}
}

// Dispatcher returns a trigger.Dispatcher over store, starter, fences and the
// doubles.
//
//nolint:ireturn // The port is the contract of the result.
func (d *Doubles) Dispatcher(store trigger.Store, starter trigger.Starter, fences trigger.Fences,
) (trigger.Dispatcher, error) {
	disp, err := trigger.NewDispatcher(trigger.DispatcherConfig{
		Store: store, Starter: starter, Savers: d, Units: d, Audit: d, Fences: fences, Versions: d,
		Now: func() time.Time { return at(0) },
	})
	if err != nil {
		return nil, fmt.Errorf("dispatcher: %w", err)
	}

	return disp, nil
}

// RevokeSaver makes the saver check refuse the person with the subject, as
// if the person lost access or the principal was deleted or disabled.
func (d *Doubles) RevokeSaver(subject string) {
	d.mu.Lock()
	defer d.mu.Unlock()

	d.revoked[subject] = true
}

// RestoreSaver undoes RevokeSaver.
func (d *Doubles) RestoreSaver(subject string) {
	d.mu.Lock()
	defer d.mu.Unlock()

	delete(d.revoked, subject)
}

// ReuseSubject makes the saver check see a new principal with the subject, as
// if the old principal was deleted and a later principal took the subject. The
// ID of the new record differs from SaverIDOf(subject), so a trigger that the
// old principal saved is refused.
func (d *Doubles) ReuseSubject(subject string) {
	d.mu.Lock()
	defer d.mu.Unlock()

	d.reused[subject] = SaverIDOf(subject) + "-reused"
}

// SaverIDOf returns the ID of the principal record that the doubles give to the
// person with the subject, and that the triggers of this package carry.
func SaverIDOf(subject string) string {
	return "prn-" + subject
}

// SaverID implements trigger.SaverCheck.
func (d *Doubles) SaverID(_ context.Context, subject string) (string, error) {
	d.mu.Lock()
	defer d.mu.Unlock()

	if d.revoked[subject] {
		return "", fmt.Errorf("%w: the saver lost access", trigger.ErrSaverDenied)
	}

	return d.idOf(subject), nil
}

// idOf returns the ID of the current record of subject. The caller holds mu.
func (d *Doubles) idOf(subject string) string {
	if id, ok := d.reused[subject]; ok {
		return id
	}

	return SaverIDOf(subject)
}

// Final records a final state version of unit and returns its ID.
func (d *Doubles) Final(_ *testing.T, unit graph.Unit) string {
	d.mu.Lock()
	defer d.mu.Unlock()

	id := d.write()
	d.last[unit] = id

	return id
}

// Intermediate records a state version inside a run of unit and returns its ID.
func (d *Doubles) Intermediate(_ *testing.T, _ graph.Unit) string {
	d.mu.Lock()
	defer d.mu.Unlock()

	return d.write()
}

// Refusals returns the audit entries that the dispatcher appended for ns.
func (d *Doubles) Refusals(ns namespace.Name) []audit.Entry {
	d.mu.Lock()
	defer d.mu.Unlock()

	return append([]audit.Entry(nil), d.audited[ns]...)
}

// CheckSave implements trigger.SaverCheck.
func (d *Doubles) CheckSave(_ context.Context, t trigger.Trigger) error { return d.check(t) }

// CheckStart implements trigger.SaverCheck.
func (d *Doubles) CheckStart(_ context.Context, t trigger.Trigger) error { return d.check(t) }

// Object implements trigger.Units. The name of a unit is its ID.
func (*Doubles) Object(_ context.Context, u graph.Unit) (identity.Object, error) {
	obj, err := identity.UnitObject(u.Tool, u.Namespace, u.ID)
	if err != nil {
		return identity.Object{}, fmt.Errorf("%w: %w", trigger.ErrUnitGone, err)
	}

	return obj.WithUnitID(u.ID), nil
}

// Find implements trigger.Units.
func (*Doubles) Find(_ context.Context, ns namespace.Name, unitID string) (graph.Unit, error) {
	return graph.Unit{Namespace: ns, Tool: toolOpenTofu, ID: unitID}, nil
}

// Append implements trigger.AuditAppender.
func (d *Doubles) Append(_ context.Context, fence namespace.Fence, e audit.Entry) (audit.Entry, error) {
	if err := e.Validate(); err != nil {
		return audit.Entry{}, fmt.Errorf("append: %w", err)
	}

	if e.Namespace != fence.Namespace {
		return audit.Entry{}, fmt.Errorf("%w: the entry names another namespace", audit.ErrInvalidEntry)
	}

	d.mu.Lock()
	defer d.mu.Unlock()

	d.audited[e.Namespace] = append(d.audited[e.Namespace], e)

	return e, nil
}

// LastFinal implements trigger.Versions.
func (d *Doubles) LastFinal(_ context.Context, unit graph.Unit) (string, bool, error) {
	d.mu.Lock()
	defer d.mu.Unlock()

	id, ok := d.last[unit]

	return id, ok, nil
}

func (d *Doubles) write() string {
	d.next++

	return fmt.Sprintf("version-%d", d.next)
}

func (d *Doubles) check(t trigger.Trigger) error {
	d.mu.Lock()
	defer d.mu.Unlock()

	if d.revoked[t.LastSaver] {
		return fmt.Errorf("%w: trigger %s: the saver lost access", trigger.ErrSaverDenied, t.ID)
	}

	if t.LastSaverID != d.idOf(t.LastSaver) {
		return fmt.Errorf("%w: trigger %s: the subject names another principal", trigger.ErrSaverDenied, t.ID)
	}

	return nil
}
