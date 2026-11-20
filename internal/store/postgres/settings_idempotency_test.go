package postgres_test

import (
	"errors"
	"net/http"
	"os"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/store/postgres"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/idempotency"
	"opendustpan.org/opendustpan/pkg/idempotency/idempotencytest"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/setting"
	"opendustpan.org/opendustpan/pkg/setting/settingtest"
)

// settingsKeyedChange returns the change input that sets test.label to value.
func settingsKeyedChange(value string) setting.ChangeInput {
	return setting.ChangeInput{
		Registry: settingtest.Registry(), Static: nil,
		Patch: setting.Patch{{Key: settingtest.KeyLabel, Value: value, Delete: false}},
		Entry: audit.Entry{
			At: time.UnixMilli(1), Namespace: audit.ServerScope, Subject: "alice", IDPSubject: "", TokenID: "",
			Action: string(identity.ActionServerManageSettings), Object: audit.ServerScope.String(), Version: "",
			ValuePath: "", ValueCount: nil, Changes: nil, SourceIP: "192.0.2.1", UserAgent: "",
			Channel: audit.ChannelAPI, Kind: audit.KindChange, Outcome: audit.OutcomeSuccess, RequestID: "",
			ObjectID: "", PrevHash: "", Hash: "", Sequence: 0, FenceVersion: 0, Reason: "", PrincipalKind: "",
			JobID: "", Origin: nil,
		},
		IfMatch: 0, Fresh: true, AtMs: 1_700_000_000_000,
	}
}

// TestSettingsKeyedReplay proves, on PostgreSQL, that a settings change that
// carries an Idempotency-Key stores its answer in the transaction of the
// change: a replay answers the stored result and a body of another request
// with the same key is refused. Each assertion fails when the store does not
// store the answer.
func TestSettingsKeyedReplay(t *testing.T) {
	t.Parallel()

	base := os.Getenv(envURL)
	if base == "" {
		t.Skipf("set %s to run the PostgreSQL settings idempotency test", envURL)
	}

	st := open(t, base)
	keys := st.Idempotency(idempotencytest.TestWrapper(t, 1))
	ref := idempotency.Ref{Principal: "alice", Method: http.MethodPatch, Path: "/api/v1/settings", Key: "key-one"}
	material := []byte("body-one")
	started, err := keys.Begin(t.Context(), ref, material, settingsKeyedNow())
	if err != nil || started.Outcome != idempotency.Started {
		t.Fatalf("begin = %+v, %v, want Started", started, err)
	}

	completion := idempotency.NewCompletion(ref, started.Token, func(result any) (idempotency.Answer, error) {
		if _, ok := result.(setting.Result); !ok {
			return idempotency.Answer{}, errSettingsKeyedResult
		}

		return idempotency.Answer{Status: http.StatusOK, Headers: nil, Body: []byte(`{"stored":true}`)}, nil
	})

	_, err = st.SettingStore().Update(
		idempotency.WithCompletion(t.Context(), completion),
		setting.InstallationScope(), namespace.Fence{Namespace: "", FailoverVersion: 0}, settingsKeyedChange("blue"),
	)
	if err != nil {
		t.Fatalf("update: %v", err)
	}

	if !completion.Stored() {
		t.Fatal("the change did not store the answer in its transaction")
	}

	checkSettingsKeyedReplay(t, st, keys, ref, material)
}

// checkSettingsKeyedReplay checks the replay, the key reuse and the stored
// revision after one keyed change.
func checkSettingsKeyedReplay(
	t *testing.T, st *postgres.Store, keys idempotency.Store, ref idempotency.Ref, material []byte,
) {
	t.Helper()

	replay, err := keys.Begin(t.Context(), ref, material, settingsKeyedNow())
	if err != nil || replay.Outcome != idempotency.Replayed || string(replay.Answer.Body) != `{"stored":true}` {
		t.Fatalf("replay = %+v, %v, want Replayed with the stored answer", replay, err)
	}

	reused, err := keys.Begin(t.Context(), ref, []byte("body-two"), settingsKeyedNow())
	if err != nil || reused.Outcome != idempotency.Reused {
		t.Fatalf("key reuse = %+v, %v, want Reused", reused, err)
	}

	stored, err := st.SettingStore().Load(t.Context(), setting.InstallationScope())
	if err != nil || stored.Revision != 1 || stored.Values[settingtest.KeyLabel] != "blue" {
		t.Fatalf("stored = %+v, %v, want revision 1 and the label blue", stored, err)
	}
}

// TestSettingsKeyedChangeRollsBackWithALostRecord proves that a change whose
// pending record is gone writes nothing: the answer and the change are one
// transaction.
func TestSettingsKeyedChangeRollsBackWithALostRecord(t *testing.T) {
	t.Parallel()

	base := os.Getenv(envURL)
	if base == "" {
		t.Skipf("set %s to run the PostgreSQL settings idempotency test", envURL)
	}

	st := open(t, base)
	keys := st.Idempotency(idempotencytest.TestWrapper(t, 1))
	ref := idempotency.Ref{Principal: "alice", Method: http.MethodPatch, Path: "/api/v1/settings", Key: "key-lost"}

	started, err := keys.Begin(t.Context(), ref, []byte("body"), settingsKeyedNow())
	if err != nil || started.Outcome != idempotency.Started {
		t.Fatalf("begin = %+v, %v, want Started", started, err)
	}

	if err = keys.Abandon(t.Context(), ref, started.Token); err != nil {
		t.Fatalf("abandon: %v", err)
	}

	completion := idempotency.NewCompletion(ref, started.Token, func(any) (idempotency.Answer, error) {
		return idempotency.Answer{Status: http.StatusOK, Headers: nil, Body: []byte("{}")}, nil
	})

	_, err = st.SettingStore().Update(
		idempotency.WithCompletion(t.Context(), completion),
		setting.InstallationScope(), namespace.Fence{Namespace: "", FailoverVersion: 0}, settingsKeyedChange("blue"),
	)
	if !errors.Is(err, idempotency.ErrLost) {
		t.Fatalf("update with a lost record = %v, want ErrLost", err)
	}

	stored, err := st.SettingStore().Load(t.Context(), setting.InstallationScope())
	if err != nil || stored.Revision != 0 || len(stored.Values) != 0 {
		t.Fatalf("stored = %+v, %v, want no change", stored, err)
	}
}

// errSettingsKeyedResult is the error of a build function that gets a result
// of another type.
var errSettingsKeyedResult = errors.New("the change passed a result of another type")

// settingsKeyedNow is the fixed instant of the keyed replay cases: no case
// reads the wall clock.
func settingsKeyedNow() time.Time {
	return time.UnixMilli(1_700_000_000_000)
}
