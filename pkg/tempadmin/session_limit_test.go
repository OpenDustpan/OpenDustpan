package tempadmin_test

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/setting"
	"opendustpan.org/opendustpan/pkg/store/sqlite"
	"opendustpan.org/opendustpan/pkg/tempadmin"
)

// stubLoader answers one fixed stored state.
type stubLoader struct {
	values map[string]string
}

func (l stubLoader) Load(context.Context, setting.Scope) (setting.Stored, error) {
	return setting.Stored{Values: l.values, Revision: 0, Digest: ""}, nil
}

// recordingAudit records the entries of AppendServer.
type recordingAudit struct {
	entries []audit.Entry
}

func (a *recordingAudit) AppendServer(_ context.Context, e audit.Entry) (audit.Entry, error) {
	a.entries = append(a.entries, e)

	return e, nil
}

func openSettingsStore(t *testing.T) *sqlite.Store {
	t.Helper()

	st, err := sqlite.Open(t.Context(), filepath.Join(t.TempDir(), "dustpan.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}

	t.Cleanup(func() {
		if closeErr := st.Close(); closeErr != nil {
			t.Errorf("close store: %v", closeErr)
		}
	})

	return st
}

// newRecordingAudit returns an empty recorder.
func newRecordingAudit() *recordingAudit {
	return &recordingAudit{entries: nil}
}

// blankEntry is an audit entry with no field set.
func blankEntry() audit.Entry {
	var e audit.Entry

	return e
}

func TestTempAdminSessionLimit(t *testing.T) {
	t.Parallel()

	at := baseTime()

	t.Run("the default ends the record after 60 minutes", func(t *testing.T) {
		t.Parallel()

		got, err := tempadmin.ResolveSessionLimit(t.Context(), tempadmin.SessionLimitSource{
			Settings: stubLoader{values: nil}, Audit: newRecordingAudit(), Entry: blankEntry(), At: at,
		})
		if err != nil || got != time.Hour {
			t.Fatalf("limit = %s, %v, want 1h", got, err)
		}

		if end := tempadmin.RecordEnd(at, got, 24*time.Hour); !end.Equal(at.Add(time.Hour)) {
			t.Fatalf("end = %s, want 60 minutes after the create", end)
		}
	})

	t.Run("the token policy maximum caps the record", func(t *testing.T) {
		t.Parallel()

		if end := tempadmin.RecordEnd(at, 2*time.Hour, 30*time.Minute); !end.Equal(at.Add(30 * time.Minute)) {
			t.Fatalf("end = %s, want 30 minutes after the create", end)
		}

		if end := tempadmin.RecordEnd(at, tempadmin.Lifetime, 400*time.Hour); !end.Equal(at.Add(tempadmin.Lifetime)) {
			t.Fatalf("end = %s, want the record lifetime", end)
		}
	})

	t.Run("a stored limit applies to the next create", func(t *testing.T) {
		t.Parallel()

		got, err := tempadmin.ResolveSessionLimit(t.Context(), tempadmin.SessionLimitSource{
			Settings: stubLoader{values: map[string]string{tempadmin.SettingSessionLimit: "2h"}},
			Audit:    newRecordingAudit(), Entry: blankEntry(), At: at,
		})
		if err != nil || got != 2*time.Hour {
			t.Fatalf("limit = %s, %v, want 2h", got, err)
		}
	})
}

func TestTempAdminSessionLimitOutsideRange(t *testing.T) {
	t.Parallel()

	at := baseTime()

	for _, bad := range []string{"48h", "-1h", "soon", "0s", "3600", "1h0m0.0001s"} {
		log := newRecordingAudit()

		_, err := tempadmin.ResolveSessionLimit(t.Context(), tempadmin.SessionLimitSource{
			Settings: stubLoader{values: map[string]string{tempadmin.SettingSessionLimit: bad}},
			Audit:    log, Entry: blankEntry(), At: at,
		})
		if !errors.Is(err, tempadmin.ErrSessionLimit) {
			t.Fatalf("limit %q: error = %v, want ErrSessionLimit", bad, err)
		}

		if len(log.entries) != 1 || log.entries[0].Outcome != audit.OutcomeFailure {
			t.Fatalf("limit %q: entries = %+v, want one failure entry", bad, log.entries)
		}
	}
}

func TestTempAdminCreateAuditNamesLimit(t *testing.T) {
	t.Parallel()

	change := tempadmin.LimitChange(tempadmin.DefaultSessionLimit)
	if change.Key != "session-limit-seconds" || change.New != "3600" {
		t.Fatalf("change = %+v, want the limit of 3600 seconds", change)
	}
}

func TestHostResetSessionLimit(t *testing.T) {
	t.Parallel()

	st := openSettingsStore(t)

	registry, err := setting.NewRegistry(tempadmin.SessionLimitSettingDeclarations())
	if err != nil {
		t.Fatalf("registry: %v", err)
	}

	svc := setting.NewService(st.SettingStore(), registry, nil, nil, nil)

	_, err = svc.Patch(t.Context(), setting.InstallationScope(),
		setting.Patch{{Key: tempadmin.SettingSessionLimit, Value: "2h", Delete: false}},
		setting.PatchRequest{Entry: resetEntry(), IfMatch: 0, Fresh: true})
	if err != nil {
		t.Fatalf("store the limit: %v", err)
	}

	err = tempadmin.ResetSessionLimit(t.Context(), st.SettingStore(), tempadmin.ResetInput{
		Mode: "bootstrap", Reason: "the stored limit is wrong", Audit: st, Entry: resetEntry(), Now: nil,
	})
	if err != nil {
		t.Fatalf("reset: %v", err)
	}

	stored, err := st.SettingStore().Load(t.Context(), setting.InstallationScope())
	if err != nil {
		t.Fatalf("load: %v", err)
	}

	if _, has := stored.Values[tempadmin.SettingSessionLimit]; has {
		t.Fatal("the stored limit stays after the reset")
	}

	listing, err := st.List(t.Context(), audit.ServerScope, audit.Page{Number: 1, Size: audit.MaxPageSize})
	if err != nil {
		t.Fatalf("list: %v", err)
	}

	found := false

	for i := range listing.Entries {
		e := listing.Entries[i]
		if e.Reason == "the stored limit is wrong" && len(e.Changes) == 1 &&
			e.Changes[0].Key == tempadmin.SettingSessionLimit {
			found = true
		}
	}

	if !found {
		t.Fatalf("no audit entry names the key and the reason: %+v", listing.Entries)
	}
}

// resetEntry is the audit entry of the host.
func resetEntry() audit.Entry {
	e := blankEntry()
	e.Subject = tempadmin.HostSubject
	e.Channel = audit.ChannelAPI
	e.Object = string(audit.ServerScope)

	return e
}

func TestResolveSessionLimitNeedsItsDependencies(t *testing.T) {
	t.Parallel()

	loader := stubLoader{values: map[string]string{}}

	tests := []struct {
		name string
		src  tempadmin.SessionLimitSource
	}{
		{
			"no settings",
			tempadmin.SessionLimitSource{
				Settings: nil,
				Audit:    newRecordingAudit(),
				Entry:    blankEntry(),
				At:       baseTime(),
			},
		},
		{
			"no audit store",
			tempadmin.SessionLimitSource{Settings: loader, Audit: nil, Entry: blankEntry(), At: baseTime()},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			_, err := tempadmin.ResolveSessionLimit(t.Context(), tc.src)
			if !errors.Is(err, tempadmin.ErrMissingDependency) {
				t.Fatalf("error = %v, want ErrMissingDependency", err)
			}
		})
	}
}
