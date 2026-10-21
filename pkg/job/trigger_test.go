package job_test

import (
	"errors"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/job"
	"opendustpan.org/opendustpan/pkg/namespace"
)

func serviceActor(t *testing.T, ns namespace.Name) job.Actor {
	t.Helper()

	p, err := identity.NamespaceService(ns)
	if err != nil {
		t.Fatalf("NamespaceService(%q) = %v", ns, err)
	}

	return job.Actor{Principal: p, Agent: "", Mode: "", ConfirmAuth: "", AuthAt: actorOf("x").AuthAt}
}

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
		"saver with a bidi mark":    {ID: "t", Saver: "ada‮"},
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
			"a manual fire by a person", func(*testing.T) job.Actor { return actorOf("ada") }, "", ref, nil,
		},
		{
			"a manual fire with the source api",
			func(*testing.T) job.Actor { return actorOf("ada") },
			job.SourceAPI,
			ref,
			nil,
		},
		{
			"a person cannot claim the source trigger", func(*testing.T) job.Actor { return actorOf("ada") },
			job.SourceTrigger, ref, job.ErrInvalid,
		},
		{
			"a person cannot claim the source schedule", func(*testing.T) job.Actor { return actorOf("ada") },
			job.SourceSchedule, ref, job.ErrInvalid,
		},
		{
			"an agent cannot fire a trigger", func(*testing.T) job.Actor {
				a := actorOf("ada")
				a.Agent = "dusty"

				return a
			}, job.SourceAPI, ref, job.ErrAgentDenied,
		},
		{
			"a start with a trigger that is not valid", func(*testing.T) job.Actor { return actorOf("ada") }, "",
			&job.TriggerRef{ID: "Bad ID", Saver: "x"}, job.ErrInvalid,
		},
		{
			"the service principal with the source trigger", func(t *testing.T) job.Actor {
				t.Helper()

				return serviceActor(t, "acme")
			}, job.SourceTrigger, ref, nil,
		},
		{
			"the service principal with the source schedule", func(t *testing.T) job.Actor {
				t.Helper()

				return serviceActor(t, "acme")
			}, job.SourceSchedule, ref, nil,
		},
		{
			"the service principal with the source forge", func(t *testing.T) job.Actor {
				t.Helper()

				return serviceActor(t, "acme")
			}, job.SourceForge, ref, nil,
		},
		{
			"the service principal with the source api", func(t *testing.T) job.Actor {
				t.Helper()

				return serviceActor(t, "acme")
			}, job.SourceAPI, ref, job.ErrInvalid,
		},
		{
			"the service principal with no source", func(t *testing.T) job.Actor {
				t.Helper()

				return serviceActor(t, "acme")
			}, "", ref, job.ErrInvalid,
		},
		{
			"the service principal with the source catalog", func(t *testing.T) job.Actor {
				t.Helper()

				return serviceActor(t, "acme")
			}, job.SourceCatalog, ref, job.ErrInvalid,
		},
		{
			"the service principal with no trigger", func(t *testing.T) job.Actor {
				t.Helper()

				return serviceActor(t, "acme")
			}, job.SourceTrigger, nil, job.ErrInvalid,
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
				if !errors.Is(err, tt.wantErr) {
					t.Fatalf("Start = %v, want %v", err, tt.wantErr)
				}

				if len(r.store.creates) != 0 {
					t.Fatalf("Create calls = %d, want none for a refused start", len(r.store.creates))
				}

				return
			}

			if err != nil {
				t.Fatalf("Start = %v", err)
			}

			if (tt.trigger == nil) != (j.Trigger == nil) {
				t.Fatalf("job trigger = %+v, request trigger = %+v", j.Trigger, tt.trigger)
			}

			if tt.trigger != nil && *j.Trigger != *tt.trigger {
				t.Fatalf("job trigger = %+v, want %+v", *j.Trigger, *tt.trigger)
			}
		})
	}
}

func TestStartTriggerAuditAndCopy(t *testing.T) {
	t.Parallel()

	r := newRig(t)
	ref := &job.TriggerRef{ID: "trg-1", Saver: "user:grace"}

	req := r.plan("")
	req.Source = job.SourceTrigger
	req.Trigger = ref

	j, err := r.svc.Start(t.Context(), serviceActor(t, "acme"), req)
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

	switch {
	case none == "" || one == "":
		t.Fatalf("hashes are empty: %q, %q", none, one)
	case one != same:
		t.Errorf("one trigger gave two hashes: %q, %q", one, same)
	case one == none, one == otherID, one == otherSaver:
		t.Errorf("a request with another trigger shares a hash: %q, %q, %q, %q", none, one, otherID, otherSaver)
	}
}
