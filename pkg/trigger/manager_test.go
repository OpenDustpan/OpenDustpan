package trigger_test

import (
	"errors"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/cron"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/job"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/trigger"
)

// managerRig holds a Manager and the fakes behind it.
type managerRig struct {
	m      *trigger.Manager
	store  *fakeStore
	authz  *fakeAuthz
	units  *fakeUnits
	savers *fakeSaverCheck
	disp   *fakeDispatcher
	fences *fakeFences
}

func newManagerRig(t *testing.T) *managerRig {
	t.Helper()

	r := &managerRig{
		m: nil, store: newStore(), authz: newAuthz(), units: newUnits(), savers: newSaverCheck(),
		disp: newDispatcher(), fences: newFences(),
	}

	// ada holds every action on units a and b.
	for _, id := range []string{"a", "b", "c"} {
		r.authz.grant(ada, id, trigger.SettingsAction, trigger.StartAction, identity.ActionUnitRead)
	}

	n := 0

	m, err := trigger.NewManager(trigger.ManagerConfig{
		Store: r.store, Dispatcher: r.disp, Authorizer: r.authz, Savers: r.savers, Units: r.units, Fences: r.fences,
		Now: func() time.Time { return time.UnixMilli(1_800_000_000_000).UTC() },
		NewID: func() trigger.ID {
			n++

			return trigger.ID("trg-" + strconv.Itoa(n))
		},
	})
	if err != nil {
		t.Fatalf("NewManager = %v", err)
	}

	r.m = m

	return r
}

func reqInfo() trigger.RequestInfo {
	return trigger.RequestInfo{
		Channel:   audit.ChannelConsole,
		SourceIP:  "192.0.2.1",
		UserAgent: "test",
		RequestID: "req-1",
	}
}

func scheduleInput(unitID string) trigger.SaveInput {
	return trigger.SaveInput{
		Namespace: acme, UnitID: unitID, Kind: trigger.KindSchedule,
		Frequency: trigger.Frequency{Preset: cron.PresetDaily, Cron: ""}, SourceID: "", Enabled: true,
	}
}

func followsInput(unitID, sourceID string) trigger.SaveInput {
	return trigger.SaveInput{
		Namespace: acme, UnitID: unitID, Kind: trigger.KindFollows, Frequency: trigger.Frequency{Preset: "", Cron: ""},
		SourceID: sourceID, Enabled: true,
	}
}

func TestNewManagerNeedsPorts(t *testing.T) {
	t.Parallel()

	r := newManagerRig(t)

	full := trigger.ManagerConfig{
		Store: r.store, Dispatcher: r.disp, Authorizer: r.authz, Savers: r.savers, Units: r.units, Fences: r.fences,
		Now: nil, NewID: nil,
	}

	for name, mutate := range map[string]func(*trigger.ManagerConfig){
		"no store":      func(c *trigger.ManagerConfig) { c.Store = nil },
		"no dispatcher": func(c *trigger.ManagerConfig) { c.Dispatcher = nil },
		"no authorizer": func(c *trigger.ManagerConfig) { c.Authorizer = nil },
		"no savers":     func(c *trigger.ManagerConfig) { c.Savers = nil },
		"no units":      func(c *trigger.ManagerConfig) { c.Units = nil },
		"no fences":     func(c *trigger.ManagerConfig) { c.Fences = nil },
	} {
		cfg := full
		mutate(&cfg)

		if _, err := trigger.NewManager(cfg); !errors.Is(err, trigger.ErrInvalid) {
			t.Errorf("%s: NewManager = %v, want ErrInvalid", name, err)
		}
	}
}

func TestManagerCreate(t *testing.T) {
	t.Parallel()

	r := newManagerRig(t)

	got, err := r.m.Create(t.Context(), person(ada), reqInfo(), scheduleInput("b"))
	if err != nil {
		t.Fatalf("Create = %v", err)
	}

	want := schedule("trg-1", trigger.Frequency{Preset: cron.PresetDaily, Cron: ""})
	want.Unit = unit("b")

	if got != want {
		t.Fatalf("Create = %+v, want %+v", got, want)
	}

	if r.store.callCount() != 1 || r.savers.saves != 1 {
		t.Fatalf("store writes %d, saver checks %d; want 1 and 1", r.store.callCount(), r.savers.saves)
	}

	checkCreateAudit(t, r.store.calls[0].entry)

	// A person cannot set the last saver: it is the caller.
	if r.store.triggers["trg-1"].LastSaver != ada {
		t.Errorf("last saver = %q, want %q", r.store.triggers["trg-1"].LastSaver, ada)
	}
}

// checkCreateAudit checks the audit entry of the first Create of ada.
func checkCreateAudit(t *testing.T, e audit.Entry) {
	t.Helper()

	got := []string{
		e.Subject, string(e.Namespace), e.Action, e.Object, string(e.Channel), e.SourceIP, e.RequestID,
		string(e.Kind), string(e.Outcome), e.TokenID,
	}
	want := []string{
		ada, string(acme), string(trigger.SettingsAction), "opentofu:acme/b", string(audit.ChannelConsole),
		"192.0.2.1", "req-1", string(audit.KindChange), string(audit.OutcomeSuccess), "tok-" + ada,
	}

	if !slices.Equal(got, want) {
		t.Errorf("audit entry = %+v", e)
	}

	keys := make([]string, 0, len(e.Changes))
	for _, ch := range e.Changes {
		keys = append(keys, ch.Key)
	}

	if !slices.Contains(keys, "trigger") || !slices.Contains(keys, "kind") || !slices.Contains(keys, "frequency") {
		t.Errorf("audit changes = %v, want the trigger, the kind and the frequency", keys)
	}
}

// noPrincipal returns the principal of a call with no sign-in.
func noPrincipal() identity.Principal {
	return identity.Principal{
		Subject: "", DisplayName: "", Admin: false, TokenID: "", TokenExpiresAt: time.Time{},
		TemporaryAdministrator: false, TemporaryExpiresAt: time.Time{}, Kind: "", Credential: "", JobID: "",
		JobUnit: identity.Object{},
	}
}

// createRefusal is one refused Create call.
type createRefusal struct {
	name   string
	setup  func(r *managerRig)
	caller identity.Principal
	in     func(t *testing.T) trigger.SaveInput
	want   error
	action identity.Action
}

// createRefusals returns the cases of TestManagerCreateRefusals.
func createRefusals(t *testing.T) []createRefusal {
	t.Helper()

	jobToken := person(ada)
	jobToken.Credential = identity.CredentialJobToken

	service, err := identity.NamespaceService(acme)
	if err != nil {
		t.Fatalf("NamespaceService = %v", err)
	}

	return []createRefusal{
		{
			name:   "no settings action",
			caller: person(ada),
			in:     func(*testing.T) trigger.SaveInput { return scheduleInput("b") },
			setup:  func(r *managerRig) { delete(r.authz.allow, ada+" "+string(trigger.SettingsAction)+" b") },
			want:   identity.ErrDenied,
			action: trigger.SettingsAction,
		},
		{
			name:   "no start action",
			caller: person(ada),
			in:     func(*testing.T) trigger.SaveInput { return scheduleInput("b") },
			setup:  func(r *managerRig) { delete(r.authz.allow, ada+" "+string(trigger.StartAction)+" b") },
			want:   identity.ErrDenied,
			action: trigger.StartAction,
		},
		{
			name:   "no read on the source",
			caller: person(ada),
			in:     func(*testing.T) trigger.SaveInput { return followsInput("b", "a") },
			setup:  func(r *managerRig) { delete(r.authz.allow, ada+" "+string(identity.ActionUnitRead)+" a") },
			want:   identity.ErrDenied,
			action: identity.ActionUnitRead,
		},
		{
			name:   "a person who holds nothing",
			caller: person(grace),
			in:     func(*testing.T) trigger.SaveInput { return scheduleInput("b") },
			setup:  nil,
			want:   identity.ErrDenied,
			action: trigger.SettingsAction,
		},
		{
			name: "a unit that does not exist is a denial too", caller: person(ada),
			in:     func(*testing.T) trigger.SaveInput { return scheduleInput("b") },
			setup:  func(r *managerRig) { r.units.gone["b"] = true },
			want:   identity.ErrDenied,
			action: trigger.SettingsAction,
		},
		{
			name: "a source that does not exist is a denial too", caller: person(ada),
			in:     func(*testing.T) trigger.SaveInput { return followsInput("b", "a") },
			setup:  func(r *managerRig) { r.units.gone["a"] = true },
			want:   identity.ErrDenied,
			action: trigger.SettingsAction,
		},
		{
			name: "a job token", caller: jobToken, in: func(*testing.T) trigger.SaveInput { return scheduleInput("b") },
			setup: nil, want: identity.ErrDenied, action: "",
		},
		{
			name:   "the namespace service principal",
			caller: service,
			in:     func(*testing.T) trigger.SaveInput { return scheduleInput("b") },
			setup:  nil,
			want:   identity.ErrDenied,
			action: "",
		},
		{
			name:   "no principal",
			caller: noPrincipal(),
			in:     func(*testing.T) trigger.SaveInput { return scheduleInput("b") },
			setup:  nil,
			want:   trigger.ErrInvalid,
			action: "",
		},
		{
			name:   "a saver that a start would refuse",
			caller: person(ada),
			in:     func(*testing.T) trigger.SaveInput { return scheduleInput("b") },
			setup:  func(r *managerRig) { r.savers.err = trigger.ErrSaverDenied },
			want:   trigger.ErrSaverDenied,
			action: "",
		},
		{
			name: "a follows trigger with no source", caller: person(ada),
			in: func(*testing.T) trigger.SaveInput {
				in := followsInput("b", "a")
				in.SourceID = ""

				return in
			},
			setup: nil, want: trigger.ErrInvalid, action: "",
		},
		{
			name: "a schedule trigger with a source", caller: person(ada),
			in: func(*testing.T) trigger.SaveInput {
				in := scheduleInput("b")
				in.SourceID = "a"

				return in
			},
			setup: nil, want: trigger.ErrInvalid, action: "",
		},
		{
			name: "a schedule that runs too fast", caller: person(ada),
			in: func(*testing.T) trigger.SaveInput {
				in := scheduleInput("b")
				in.Frequency = trigger.Frequency{Preset: "", Cron: "* * * * *"}

				return in
			},
			setup: nil, want: trigger.ErrInvalid, action: "",
		},
		{
			name: "a body with no unit", caller: person(ada),
			in: func(*testing.T) trigger.SaveInput {
				in := scheduleInput("")

				return in
			},
			setup: nil, want: trigger.ErrInvalid, action: "",
		},
		{
			name:   "a store that fails",
			caller: person(ada),
			in:     func(*testing.T) trigger.SaveInput { return scheduleInput("b") },
			setup:  func(r *managerRig) { r.store.putErr = namespace.ErrFenced },
			want:   namespace.ErrFenced,
			action: "",
		},
		{
			name:   "a fence that fails",
			caller: person(ada),
			in:     func(*testing.T) trigger.SaveInput { return scheduleInput("b") },
			setup:  func(r *managerRig) { r.fences.err = namespace.ErrNotFound },
			want:   namespace.ErrNotFound,
			action: "",
		},
	}
}

func TestManagerCreateRefusals(t *testing.T) {
	t.Parallel()

	for _, tt := range createRefusals(t) {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			r := newManagerRig(t)
			if tt.setup != nil {
				tt.setup(r)
			}

			_, err := r.m.Create(t.Context(), tt.caller, reqInfo(), tt.in(t))
			wantErr(t, "Create", err, tt.want)

			if r.store.callCount() != 0 {
				t.Errorf("a refused save wrote %d times", r.store.callCount())
			}

			checkDenied(t, err, tt.action)
		})
	}
}

// checkDenied checks that err is a *DeniedError for action, when action is set.
func checkDenied(t *testing.T, err error, action identity.Action) {
	t.Helper()

	var denied *trigger.DeniedError
	if action != "" {
		if !errors.As(err, &denied) || denied.Action != action {
			t.Errorf("Create = %v, want a *DeniedError for %s", err, action)
		}
	}
}

func TestManagerCreateCycle(t *testing.T) {
	t.Parallel()

	r := newManagerRig(t)

	if _, err := r.m.Create(t.Context(), person(ada), reqInfo(), followsInput("b", "a")); err != nil {
		t.Fatalf("Create b follows a = %v", err)
	}

	_, err := r.m.Create(t.Context(), person(ada), reqInfo(), followsInput("a", "b"))
	wantErr(t, "a follows b", err, trigger.ErrCycle)

	var cycle *trigger.CycleError
	if !errors.As(err, &cycle) {
		t.Fatalf("Create = %v, want a *CycleError", err)
	}

	if got := cycle.Error(); got != "trigger closes a loop of units: b -> a -> b" {
		t.Errorf("cycle text = %q, want the names of the units", got)
	}

	if len(cycle.Names) != 2 {
		t.Errorf("names = %v, want a name for each unit", cycle.Names)
	}
}

func TestManagerReplace(t *testing.T) {
	t.Parallel()

	r := newManagerRig(t)

	first, err := r.m.Create(t.Context(), person(ada), reqInfo(), scheduleInput("b"))
	if err != nil {
		t.Fatalf("Create = %v", err)
	}

	r.resolveGrace()

	in := scheduleInput("b")
	in.Enabled = false

	got, err := r.m.Replace(t.Context(), person(grace), reqInfo(), first.ID, in)
	if err != nil {
		t.Fatalf("Replace = %v", err)
	}

	if got.ID != first.ID || got.Enabled || got.LastSaver != grace {
		t.Errorf("Replace = %+v, want the same ID, off, saved by %s", got, grace)
	}

	// The unit and the kind do not change.
	_, err = r.m.Replace(t.Context(), person(ada), reqInfo(), first.ID, scheduleInput("c"))
	wantErr(t, "another unit", err, trigger.ErrInvalid)

	_, err = r.m.Replace(t.Context(), person(ada), reqInfo(), first.ID, followsInput("b", "a"))
	wantErr(t, "another kind", err, trigger.ErrInvalid)

	_, err = r.m.Replace(t.Context(), person(ada), reqInfo(), "trg-nope", scheduleInput("b"))
	wantErr(t, "an unknown trigger", err, trigger.ErrNotFound)

	other := scheduleInput("b")
	other.Namespace = "other"

	_, err = r.m.Replace(t.Context(), person(ada), reqInfo(), first.ID, other)
	wantErr(t, "a body for another namespace", err, trigger.ErrNotFound)

	// A person without the actions on the unit of the trigger cannot replace it,
	// whatever unit the body names.
	r.authz.grant(grace, "c", trigger.SettingsAction, trigger.StartAction)
	delete(r.authz.allow, grace+" "+string(trigger.SettingsAction)+" b")

	_, err = r.m.Replace(t.Context(), person(grace), reqInfo(), first.ID, scheduleInput("c"))
	wantErr(t, "a body for a unit the caller may change", err, identity.ErrDenied)
}

// resolveGrace gives grace the actions on unit b.
func (r *managerRig) resolveGrace() {
	r.authz.grant(grace, "b", trigger.SettingsAction, trigger.StartAction, identity.ActionUnitRead)
}

func TestManagerReads(t *testing.T) {
	t.Parallel()

	r := newManagerRig(t)

	created, err := r.m.Create(t.Context(), person(ada), reqInfo(), scheduleInput("b"))
	if err != nil {
		t.Fatalf("Create = %v", err)
	}

	got, obj, err := r.m.Get(t.Context(), person(ada), acme, created.ID)
	if err != nil || got != created || obj.UnitName() != "b" {
		t.Errorf("Get = %+v, %v, %v; want the trigger and its unit", got, obj, err)
	}

	list, err := r.m.List(t.Context(), person(ada), acme, "b")
	if err != nil || len(list) != 1 || list[0] != created {
		t.Errorf("List = %+v, %v; want the trigger", list, err)
	}

	// A caller without unit:read learns nothing: the answer is the answer for a
	// trigger that does not exist.
	_, _, err = r.m.Get(t.Context(), person(grace), acme, created.ID)
	wantErr(t, "Get without read", err, trigger.ErrNotFound)

	_, err = r.m.List(t.Context(), person(grace), acme, "b")
	wantErr(t, "List without read", err, trigger.ErrNotFound)

	_, _, err = r.m.Get(t.Context(), person(ada), acme, "trg-nope")
	wantErr(t, "Get of an unknown trigger", err, trigger.ErrNotFound)

	r.units.gone["b"] = true

	_, _, err = r.m.Get(t.Context(), person(ada), acme, created.ID)
	wantErr(t, "Get of a trigger of a deleted unit", err, trigger.ErrNotFound)

	_, err = r.m.List(t.Context(), person(ada), acme, "b")
	wantErr(t, "List of a deleted unit", err, trigger.ErrNotFound)
}

func TestManagerDelete(t *testing.T) {
	t.Parallel()

	r := newManagerRig(t)

	created, err := r.m.Create(t.Context(), person(ada), reqInfo(), scheduleInput("b"))
	if err != nil {
		t.Fatalf("Create = %v", err)
	}

	// Reading is not enough to delete.
	r.authz.grant(grace, "b", identity.ActionUnitRead)

	wantErr(t, "Delete without the settings action",
		r.m.Delete(t.Context(), person(grace), reqInfo(), acme, created.ID), identity.ErrDenied)

	if r.store.callCount() != 1 {
		t.Fatalf("store writes = %d, want only the create", r.store.callCount())
	}

	wantErr(t, "Delete of an unknown trigger", r.m.Delete(t.Context(), person(ada), reqInfo(), acme, "trg-nope"),
		trigger.ErrNotFound)

	if err := r.m.Delete(t.Context(), person(ada), reqInfo(), acme, created.ID); err != nil {
		t.Fatalf("Delete = %v", err)
	}

	last := r.store.calls[len(r.store.calls)-1]
	if !last.deleted || last.entry.Subject != ada || last.entry.Action != string(trigger.SettingsAction) ||
		last.entry.Reason != "delete trigger" {
		t.Errorf("last store call = %+v, want a delete by %s", last, ada)
	}

	wantErr(t, "Delete by a job token", r.m.Delete(t.Context(), func() identity.Principal {
		p := person(ada)
		p.Credential = identity.CredentialJobToken

		return p
	}(), reqInfo(), acme, created.ID), identity.ErrDenied)
}

func noLocation() job.Location {
	return job.Location{Connection: "", Repository: "", Ref: "", Directory: "", Revision: ""}
}

func TestManagerFireLocation(t *testing.T) {
	t.Parallel()

	for name, loc := range map[string]job.Location{
		"a repository with no ref":       {Connection: "", Repository: "acme/infra", Ref: "", Directory: "", Revision: ""},
		"a ref with no repository":       {Connection: "", Repository: "", Ref: "main", Directory: "", Revision: ""},
		"a directory above the checkout": {Connection: "", Repository: "acme/infra", Ref: "main", Directory: "../x", Revision: ""},
		"a revision that is no commit":   {Connection: "", Repository: "acme/infra", Ref: "main", Directory: "", Revision: "zz"},
	} {
		r := newManagerRig(t)

		created, err := r.m.Create(t.Context(), person(ada), reqInfo(), scheduleInput("b"))
		if err != nil {
			t.Fatalf("Create = %v", err)
		}

		_, err = r.m.Fire(
			t.Context(),
			person(ada),
			acme,
			created.ID,
			trigger.FireInput{
				Reason:         "",
				Location:       loc,
				IdempotencyKey: "",
				Request:        trigger.RequestInfo{Channel: "", SourceIP: "", UserAgent: "", RequestID: ""},
			},
		)
		wantErr(t, "Fire with "+name, err, trigger.ErrInvalid)

		if len(r.disp.actors) != 0 {
			t.Errorf("Fire with %s reached the dispatcher", name)
		}
	}
}

// checkFireDispatch checks that d got one manual fire of the trigger id by ada.
func checkFireDispatch(t *testing.T, d *fakeDispatcher, id trigger.ID, loc job.Location) {
	t.Helper()

	if len(d.actors) != 1 || d.actors[0].Principal.Subject != ada || d.actors[0].Agent != "" ||
		d.reqs[0].Trigger != id || d.reqs[0].Namespace != acme || d.reqs[0].Reason != "test run" ||
		d.reqs[0].Now.IsZero() || d.reqs[0].Location != loc {
		t.Errorf("dispatcher calls = %+v %+v, want the caller as the actor", d.actors, d.reqs)
	}
}

// fireInvalidReasons checks that Fire refuses each reason that is not valid.
func fireInvalidReasons(t *testing.T, r *managerRig, id trigger.ID) {
	t.Helper()

	for name, reason := range map[string]string{
		"a long reason":                 strings.Repeat("x", trigger.MaxFireReasonLen+1),
		"a reason with a line break":    "a\nb",
		"a reason with a bidi override": "a\u202eb",
		"a reason that is not UTF-8":    "a\xffb",
	} {
		_, err := r.m.Fire(
			t.Context(),
			person(ada),
			acme,
			id,
			trigger.FireInput{
				Reason:         reason,
				Location:       noLocation(),
				IdempotencyKey: "",
				Request:        trigger.RequestInfo{Channel: "", SourceIP: "", UserAgent: "", RequestID: ""},
			},
		)
		wantErr(t, "Fire with "+name, err, trigger.ErrInvalid)
	}
}

func TestManagerFire(t *testing.T) {
	t.Parallel()

	r := newManagerRig(t)

	created, err := r.m.Create(t.Context(), person(ada), reqInfo(), scheduleInput("b"))
	if err != nil {
		t.Fatalf("Create = %v", err)
	}

	loc := job.Location{Connection: "", Repository: "acme/infra", Ref: "main", Directory: "net", Revision: ""}

	id, err := r.m.Fire(
		t.Context(),
		person(ada),
		acme,
		created.ID,
		trigger.FireInput{
			Reason:         "test run",
			Location:       loc,
			IdempotencyKey: "",
			Request:        trigger.RequestInfo{Channel: "", SourceIP: "", UserAgent: "", RequestID: ""},
		},
	)
	if err != nil || id != "job-1" {
		t.Fatalf("Fire = %q, %v; want job-1", id, err)
	}

	checkFireDispatch(t, r.disp, created.ID, loc)

	// A caller without unit:read gets the answer for a trigger that does not
	// exist, and no start.
	_, err = r.m.Fire(
		t.Context(),
		person(grace),
		acme,
		created.ID,
		trigger.FireInput{
			Reason:         "",
			Location:       noLocation(),
			IdempotencyKey: "",
			Request:        trigger.RequestInfo{Channel: "", SourceIP: "", UserAgent: "", RequestID: ""},
		},
	)
	wantErr(t, "Fire without read", err, trigger.ErrNotFound)

	_, err = r.m.Fire(
		t.Context(),
		person(ada),
		acme,
		"trg-nope",
		trigger.FireInput{
			Reason:         "",
			Location:       noLocation(),
			IdempotencyKey: "",
			Request:        trigger.RequestInfo{Channel: "", SourceIP: "", UserAgent: "", RequestID: ""},
		},
	)
	wantErr(t, "Fire of an unknown trigger", err, trigger.ErrNotFound)

	service, err := identity.NamespaceService(acme)
	if err != nil {
		t.Fatalf("NamespaceService = %v", err)
	}

	_, err = r.m.Fire(
		t.Context(),
		service,
		acme,
		created.ID,
		trigger.FireInput{
			Reason:         "",
			Location:       noLocation(),
			IdempotencyKey: "",
			Request:        trigger.RequestInfo{Channel: "", SourceIP: "", UserAgent: "", RequestID: ""},
		},
	)
	wantErr(t, "Fire by the service principal", err, identity.ErrDenied)

	fireInvalidReasons(t, r, created.ID)

	r.disp.err = job.ErrFrozen

	_, err = r.m.Fire(
		t.Context(),
		person(ada),
		acme,
		created.ID,
		trigger.FireInput{
			Reason:         "",
			Location:       noLocation(),
			IdempotencyKey: "",
			Request:        trigger.RequestInfo{Channel: "", SourceIP: "", UserAgent: "", RequestID: ""},
		},
	)
	wantErr(t, "Fire during a freeze", err, job.ErrFrozen)

	if len(r.disp.actors) != 2 {
		t.Errorf("dispatcher calls = %d, want 2 (the refused callers never reach it)", len(r.disp.actors))
	}
}
