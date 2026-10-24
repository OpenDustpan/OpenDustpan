package job_test

import (
	"errors"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/job"
)

func serviceActor(t *testing.T) job.Actor {
	t.Helper()

	p, err := identity.NamespaceService("acme")
	if err != nil {
		t.Fatalf("NamespaceService(acme) = %v", err)
	}

	return job.Actor{Principal: p, Agent: "", Mode: "", ConfirmAuth: "", AuthAt: actorOf("x").AuthAt}
}

// adaActor returns the actor of a person.
func adaActor(*testing.T) job.Actor { return actorOf("ada") }

func TestTriggerRefValidate(t *testing.T) {
	t.Parallel()

	valid := []job.TriggerRef{
		{ID: "t", Saver: "s"},
		{ID: "trg_01-x", Saver: "user:ada"},
		{ID: strings.Repeat("a", job.MaxTriggerIDBytes), Saver: strings.Repeat("s", job.MaxTriggerSaverBytes)},
	}

	for _, ref := range valid {
		if err := ref.Validate(); err != nil {
			t.Errorf("Validate(%+v) = %v", ref, err)
		}
	}

	invalid := map[string]job.TriggerRef{
		"empty ID":                  {ID: "", Saver: "ada"},
		"long ID":                   {ID: strings.Repeat("a", job.MaxTriggerIDBytes+1), Saver: "ada"},
		"upper case ID":             {ID: "Trg", Saver: "ada"},
		"ID with a slash":           {ID: "a/b", Saver: "ada"},
		"ID with a newline":         {ID: "a\n", Saver: "ada"},
		"ID with a NUL":             {ID: "a\x00", Saver: "ada"},
		"empty saver":               {ID: "t", Saver: ""},
		"long saver":                {ID: "t", Saver: strings.Repeat("s", job.MaxTriggerSaverBytes+1)},
		"saver with a newline":      {ID: "t", Saver: "ada\nroot"},
		"saver with a bidi mark":    {ID: "t", Saver: "ada\u202e"},
		"saver not UTF-8":           {ID: "t", Saver: "ada\xff"},
		"saver is a service":        {ID: "t", Saver: "system:namespace-service:acme"},
		"saver is a service, cased": {ID: "t", Saver: "System:Namespace-Service:acme"},
	}

	for name, ref := range invalid {
		if err := ref.Validate(); !errors.Is(err, job.ErrInvalid) {
			t.Errorf("%s: Validate(%+v) = %v, want ErrInvalid", name, ref, err)
		}
	}
}

func TestStartTrigger(t *testing.T) {
	t.Parallel()

	ref := &job.TriggerRef{ID: "trg-1", Saver: "user:grace"}

	tests := []struct {
		name    string
		actor   func(t *testing.T) job.Actor
		source  job.SourceKind
		trigger *job.TriggerRef
		wantErr error
	}{
		{
			"a manual fire by a person", adaActor, "", ref, nil,
		},
		{
			"a manual fire with the source api",
			adaActor,
			job.SourceAPI,
			ref,
			nil,
		},
		{
			"a person cannot claim the source trigger", adaActor,
			job.SourceTrigger, ref, job.ErrInvalid,
		},
		{
			"a person cannot claim the source schedule", adaActor,
			job.SourceSchedule, ref, job.ErrInvalid,
		},
		{
			"an agent cannot fire a trigger", func(*testing.T) job.Actor {
				a := actorOf("ada")
				a.Agent = "dusty"

				return a
			}, job.SourceAPI, ref, job.ErrInvalid,
		},
		{
			"a start with a trigger that is not valid", adaActor, "",
			&job.TriggerRef{ID: "Bad ID", Saver: "x"}, job.ErrInvalid,
		},
		{
			"the service principal with the source trigger", serviceActor, job.SourceTrigger, ref, nil,
		},
		{
			"the service principal with the source schedule", serviceActor, job.SourceSchedule, ref, nil,
		},
		{
			"the service principal with the source forge", serviceActor, job.SourceForge, ref, nil,
		},
		{
			"the service principal with the source api", serviceActor, job.SourceAPI, ref, job.ErrInvalid,
		},
		{
			"the service principal with no source", serviceActor, "", ref, job.ErrInvalid,
		},
		{
			"the service principal with the source catalog", serviceActor, job.SourceCatalog, ref, job.ErrInvalid,
		},
		{
			"the service principal with no trigger", serviceActor, job.SourceTrigger, nil, job.ErrInvalid,
		},
		{
			"a person with the service subject and no server credential", func(*testing.T) job.Actor {
				return actorOf("system:namespace-service:acme")
			}, job.SourceTrigger, ref, identity.ErrDenied,
		},
		{
			"a person with the service subject in other case", func(*testing.T) job.Actor {
				return actorOf("SYSTEM:namespace-service:acme")
			}, "", nil, identity.ErrDenied,
		},
		{
			"an administrator with the service subject", func(*testing.T) job.Actor {
				a := actorOf("system:namespace-service:acme")
				a.Principal.Admin = true
				a.Principal.Credential = identity.CredentialAccessToken

				return a
			}, job.SourceTrigger, ref, identity.ErrDenied,
		},
		{
			"the server credential on a person", func(*testing.T) job.Actor {
				a := actorOf("ada")
				a.Principal.Credential = identity.CredentialServer

				return a
			}, "", nil, nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			r := newRig(t)
			req := r.plan("")
			req.Source = tt.source
			req.Trigger = tt.trigger

			j, err := r.svc.Start(t.Context(), tt.actor(t), req)

			if tt.wantErr != nil {
				checkTriggerStartRefused(t, r, err, tt.wantErr)

				return
			}

			checkStartTrigger(t, err, tt.trigger, j.Trigger)
		})
	}
}

// checkTriggerStartRefused checks that err is wantErr and that the start wrote nothing.
func checkTriggerStartRefused(t *testing.T, r *rig, err, wantErr error) {
	t.Helper()

	if !errors.Is(err, wantErr) {
		t.Fatalf("Start = %v, want %v", err, wantErr)
	}

	if len(r.store.creates) != 0 {
		t.Fatalf("Create calls = %d, want none for a refused start", len(r.store.creates))
	}
}

// checkStartTrigger checks that a start with the trigger ref want gave a job
// with the trigger ref got.
func checkStartTrigger(t *testing.T, err error, want, got *job.TriggerRef) {
	t.Helper()

	if err != nil {
		t.Fatalf("Start = %v", err)
	}

	if (want == nil) != (got == nil) {
		t.Fatalf("job trigger = %+v, request trigger = %+v", got, want)
	}

	if want != nil && *got != *want {
		t.Fatalf("job trigger = %+v, want %+v", *got, *want)
	}
}

func TestStartTriggerAuditAndCopy(t *testing.T) {
	t.Parallel()

	r := newRig(t)
	ref := &job.TriggerRef{ID: "trg-1", Saver: "user:grace"}

	req := r.plan("")
	req.Source = job.SourceTrigger
	req.Trigger = ref

	j, err := r.svc.Start(t.Context(), serviceActor(t), req)
	if err != nil {
		t.Fatalf("Start = %v", err)
	}

	// The job holds a copy: a later change of the request changes nothing.
	ref.ID = "changed"

	if j.Trigger == nil || j.Trigger.ID != "trg-1" {
		t.Fatalf("job trigger = %+v, want a copy with ID trg-1", j.Trigger)
	}

	if len(r.store.creates) != 1 || len(r.store.creates[0].entries) != 1 {
		t.Fatalf("Create calls = %+v, want one call with one audit entry", r.store.creates)
	}

	e := r.store.creates[0].entries[0]
	if e.Subject != identity.NamespaceServiceSubject("acme") {
		t.Errorf("audit subject = %q, want the namespace service principal", e.Subject)
	}

	got := map[string]string{}
	for _, ch := range e.Changes {
		got[ch.Key] = ch.New
	}

	if got["trigger"] != "trg-1" || got["trigger-saver"] != "user:grace" || got["source"] != "trigger" {
		t.Errorf("audit changes = %v, want the trigger, its saver and the source", got)
	}
}

func TestRequestHashTrigger(t *testing.T) {
	t.Parallel()

	hash := func(source job.SourceKind, ref *job.TriggerRef) string {
		t.Helper()

		r := newRig(t)
		req := r.plan("same-key")
		req.Source = source
		req.Trigger = ref

		j, err := r.svc.Start(t.Context(), actorOf("ada"), req)
		if err != nil {
			t.Fatalf("Start = %v", err)
		}

		return j.RequestHash
	}

	none := hash("", nil)
	one := hash("", &job.TriggerRef{ID: "trg-1", Saver: "user:ada"})
	same := hash("", &job.TriggerRef{ID: "trg-1", Saver: "user:ada"})
	otherID := hash("", &job.TriggerRef{ID: "trg-2", Saver: "user:ada"})
	otherSaver := hash("", &job.TriggerRef{ID: "trg-1", Saver: "user:bo"})

	if none == "" || one == "" {
		t.Fatalf("hashes are empty: %q, %q", none, one)
	}

	if one != same {
		t.Errorf("one trigger gave two hashes: %q, %q", one, same)
	}

	if one == none || one == otherID || one == otherSaver {
		t.Errorf("a request with another trigger shares a hash: %q, %q, %q, %q", none, one, otherID, otherSaver)
	}
}
