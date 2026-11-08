package settingtest

import (
	"errors"
	"time"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/setting"
)

// The keys of the test registry.
const (
	KeyLimit    = "test.limit"
	KeyLabel    = "test.label"
	KeyEnabled  = "test.enabled"
	KeySecret   = "test.secret"
	KeyFixed    = "test.fixed"
	KeyNSFlag   = "test.nsFlag"
	KeyNSLimit  = "test.nsLimit"
	subjectName = "alice"
	testTimeMs  = 1_700_000_000_000
)

// errLimitRange is the error of the check of KeyLimit.
var errLimitRange = errors.New("the limit is not from 0 to 9999")

// Registry returns the registry that the suite uses: an int, a string, a
// bool whose change from true to false reduces security, a secret that only
// the static layer can set, a static-only string, and two namespace
// settings.
func Registry() *setting.Registry {
	reg, err := setting.NewRegistry(Declarations())
	if err != nil {
		panic("settingtest: " + err.Error())
	}

	return reg
}

// Declarations returns the declarations of the registry of the suite.
func Declarations() []setting.Declaration {
	limit := database(KeyLimit, setting.KindInt, "10", setting.ApplyLive)
	limit.Check = func(v string) error {
		if len(v) > 4 || v[0] == '-' {
			return errLimitRange
		}

		return nil
	}

	label := database(KeyLabel, setting.KindString, "", setting.ApplyRestart)

	enabled := database(KeyEnabled, setting.KindBool, "false", setting.ApplyLive)
	enabled.ReducesSecurity = turnedOff

	secret := database(KeySecret, setting.KindString, "", setting.ApplyRestart)
	secret.Secret, secret.Database = true, false

	fixed := database(KeyFixed, setting.KindString, "x", setting.ApplyRestart)
	fixed.Database = false

	nsFlag := database(KeyNSFlag, setting.KindBool, "false", setting.ApplyLive)
	nsFlag.Level, nsFlag.ReducesSecurity = setting.LevelNamespace, turnedOff

	nsLimit := database(KeyNSLimit, setting.KindInt, "5", setting.ApplyLive)
	nsLimit.Level = setting.LevelNamespace

	return []setting.Declaration{limit, label, enabled, secret, fixed, nsFlag, nsLimit}
}

// turnedOff is the security rule of a switch: from on to off reduces security.
func turnedOff(old, next string) bool { return old == "true" && next == "false" }

// database returns an installation setting that the database layer can hold.
func database(key string, kind setting.Kind, def string, apply setting.Apply) setting.Declaration {
	return setting.Declaration{
		Key: key, Level: setting.LevelInstallation, Kind: kind, Default: def, Secret: false,
		Database: true, StaticCredentialHost: false, Apply: apply, Check: nil, ReducesSecurity: nil,
		Doc: "A setting of the test suite.",
	}
}

// StaticMap is a static layer from a map of values.
type StaticMap map[string]setting.StaticValue

// Lookup implements setting.Static.
func (m StaticMap) Lookup(key string) (setting.StaticValue, bool) {
	v, ok := m[key]

	return v, ok
}

// File returns a static value from the configuration file.
func File(value string) setting.StaticValue {
	return setting.StaticValue{Source: setting.SourceFile, Value: value, By: "key of the configuration file"}
}

// entry returns a valid audit entry for a change in scope.
func entry(scope setting.Scope) audit.Entry {
	ns, object := audit.ServerScope, audit.ServerScope.String()
	if !scope.IsInstallation() {
		ns, object = scope.Namespace, scope.Namespace.String()
	}

	return audit.Entry{
		At: time.UnixMilli(1), Namespace: ns, Subject: subjectName, IDPSubject: "", TokenID: "",
		Action: actionOf(scope), Object: object, Version: "", ValuePath: "", ValueCount: nil, Changes: nil,
		SourceIP: "192.0.2.1", UserAgent: "", Channel: audit.ChannelAPI, Kind: audit.KindChange,
		Outcome: audit.OutcomeSuccess, RequestID: "", ObjectID: "", PrevHash: "", Hash: "", Sequence: 0,
		FenceVersion: 0, Reason: "", PrincipalKind: "", JobID: "",
	}
}

// input returns a change input over the test registry.
func input(
	static setting.Static,
	scope setting.Scope,
	ifMatch int64,
	fresh bool,
	edits ...setting.Edit,
) setting.ChangeInput {
	return setting.ChangeInput{
		Registry: Registry(), Static: static, Patch: edits, Entry: entry(scope), IfMatch: ifMatch, Fresh: fresh,
		AtMs: testTimeMs,
	}
}

// set returns the edit that sets key to value.
func set(key, value string) setting.Edit { return setting.Edit{Key: key, Value: value, Delete: false} }

// del returns the edit that deletes the database value of key.
func del(key string) setting.Edit { return setting.Edit{Key: key, Value: "", Delete: true} }

// staleFence returns a fence of the namespace of f that is not the current one.
func staleFence(f namespace.Fence) namespace.Fence {
	return namespace.Fence{Namespace: f.Namespace, FailoverVersion: f.FailoverVersion + 1}
}

// actionOf returns the audit action of a change of scope.
func actionOf(scope setting.Scope) string {
	if scope.IsInstallation() {
		return string(identity.ActionServerManageSettings)
	}

	return string(identity.ActionNamespaceManageSettings)
}
