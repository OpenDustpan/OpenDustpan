package unitsettings_test

import (
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/fakecred"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/job"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/unitsettings"
)

const (
	testPool       = "rp-0123456789abcdef"
	testConnection = "gitconn-0123456789abcdef0123456789abcdef"
	refMain        = "main"
)

// planStep returns a valid custom step.
func planStep(env map[string]string) job.Step {
	return job.Step{
		Name: "plan", Role: job.RoleTool, Phase: job.PhasePlan, Args: []string{"tofu", "plan"}, Env: env, Dir: "",
		Timeout: 0, AllowFailure: false, ChangesResources: false, Outputs: nil,
	}
}

// poolDefaults returns defaults that name pool.
func poolDefaults(pool string) unitsettings.NamespaceDefaults {
	var d unitsettings.NamespaceDefaults

	d.Pool = pool

	return d
}

func testUnit(t *testing.T) identity.Object {
	t.Helper()

	ns, err := namespace.ParseName("acme")
	if err != nil {
		t.Fatal(err)
	}

	u, err := identity.UnitObject("pulumi", ns, "web/prod")
	if err != nil {
		t.Fatal(err)
	}

	return u.WithUnitID("unit-1")
}

func validSettings(t *testing.T) unitsettings.Settings {
	t.Helper()

	return unitsettings.Settings{
		Unit: testUnit(t), Executor: job.ExecutorRunner, Pool: testPool,
		CodeLocation: unitsettings.CodeLocation{
			Connection: testConnection, Repository: "acme/infra", Ref: refMain, Directory: "stacks/prod",
		},
		WriteBack:      unitsettings.WriteBack{Delivery: job.DeliveryDirectPush, Files: []string{"Pulumi.prod.yaml"}},
		ReviewRequests: unitsettings.ReviewRequests{Enabled: true}, Steps: nil, Version: 0, UpdatedAt: time.Time{},
	}
}

type hostileCase struct {
	name   string
	mutate func(s *unitsettings.Settings)
}

// hostileCases are values that Validate must refuse.
func hostileCases() []hostileCase {
	many := make([]string, unitsettings.MaxFiles+1)
	for i := range many {
		many[i] = "f" + strings.Repeat("x", i) + ".yaml"
	}

	return []hostileCase{
		{"unknown executor", func(s *unitsettings.Settings) { s.Executor = "shell" }},
		{"pool that is not a pool ID", func(s *unitsettings.Settings) { s.Pool = "pool; drop" }},
		{"pool with an empty ID", func(s *unitsettings.Settings) { s.Pool = "rp-" }},
		{"connection that is not an ID", func(s *unitsettings.Settings) { s.CodeLocation.Connection = "github" }},
		{"directory that climbs out", func(s *unitsettings.Settings) { s.CodeLocation.Directory = "../x" }},
		{"absolute directory", func(s *unitsettings.Settings) { s.CodeLocation.Directory = "/etc" }},
		{"root spelled as one period", func(s *unitsettings.Settings) { s.CodeLocation.Directory = "." }},
		{"directory in CI configuration", func(s *unitsettings.Settings) { s.CodeLocation.Directory = "a/.github/b" }},
		{"directory in CI configuration with no file", func(s *unitsettings.Settings) {
			s.CodeLocation.Directory, s.WriteBack.Files = "a/.github/b", nil
		}},
		{"directory with a control character", func(s *unitsettings.Settings) { s.CodeLocation.Directory = "a\nb" }},
		{"ref with a leading dash", func(s *unitsettings.Settings) { s.CodeLocation.Ref = "--upload-pack=x" }},
		{"ref with a control character", func(s *unitsettings.Settings) { s.CodeLocation.Ref = "main\x00" }},
		{
			"repository with a user name",
			func(s *unitsettings.Settings) {
				s.CodeLocation.Repository = fakecred.URL("https", "u", "p", "host", "/a/b")
			},
		},
		{
			"repository with a metadata address host",
			func(s *unitsettings.Settings) { s.CodeLocation.Repository = "https://169.254.169.254/a/b" },
		},
		{
			"repository with an IPv6 host",
			func(s *unitsettings.Settings) { s.CodeLocation.Repository = "https://[fd00::1]/a/b" },
		},
		{
			"repository with a localhost host",
			func(s *unitsettings.Settings) { s.CodeLocation.Repository = "https://localhost/a/b" },
		},
		{
			"repository that is an scp address",
			func(s *unitsettings.Settings) { s.CodeLocation.Repository = "git@host:a/b" },
		},
		{"unknown delivery", func(s *unitsettings.Settings) { s.WriteBack.Delivery = "force-push" }},
		{
			"file outside the directory",
			func(s *unitsettings.Settings) { s.WriteBack.Files = []string{"../secrets.yaml"} },
		},
		{"absolute file", func(s *unitsettings.Settings) { s.WriteBack.Files = []string{"/etc/passwd"} }},
		{
			"file in CI configuration",
			func(s *unitsettings.Settings) { s.WriteBack.Files = []string{".github/workflows/x.yml"} },
		},
		{"same file twice", func(s *unitsettings.Settings) { s.WriteBack.Files = []string{"a.yaml", "a.yaml"} }},
		{"too many files", func(s *unitsettings.Settings) { s.WriteBack.Files = many }},
		{"step that sets changes resources", func(s *unitsettings.Settings) {
			step := planStep(nil)
			step.ChangesResources = true
			s.Steps = []job.Step{step}
		}},
		{"step with a credential variable", func(s *unitsettings.Settings) {
			s.Steps = []job.Step{planStep(map[string]string{"AWS_SECRET_ACCESS_KEY": "x"})}
		}},
		{"unit with no stable ID", func(s *unitsettings.Settings) { s.Unit = s.Unit.WithUnitID("") }},
		{
			"unit that is a namespace",
			func(s *unitsettings.Settings) { s.Unit = identity.NamespaceObject(s.Unit.Namespace()) },
		},
	}
}

// TestValidateRefusesHostileSettings sends hostile values straight to Validate,
// as if the route had not checked them.
func TestValidateRefusesHostileSettings(t *testing.T) {
	t.Parallel()

	for _, tc := range hostileCases() {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			s := validSettings(t)
			tc.mutate(&s)

			if err := s.Validate(); !errors.Is(err, unitsettings.ErrInvalid) {
				t.Fatalf("Validate = %v, want ErrInvalid", err)
			}
		})
	}
}

// TestMarshalRefusesHostileSettings sends the same values to the encoder of the
// stored document, as if Validate had not run.
func TestMarshalRefusesHostileSettings(t *testing.T) {
	t.Parallel()

	for _, tc := range hostileCases() {
		if strings.HasPrefix(tc.name, "unit ") {
			continue
		}

		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			s := validSettings(t)
			tc.mutate(&s)

			if _, err := unitsettings.Marshal(&s); !errors.Is(err, unitsettings.ErrInvalid) {
				t.Fatalf("Marshal = %v, want ErrInvalid", err)
			}
		})
	}
}

func TestValidateAcceptsEmptyAndFullSettings(t *testing.T) {
	t.Parallel()

	empty := validSettings(t)
	var (
		location unitsettings.CodeLocation
		write    unitsettings.WriteBack
		review   unitsettings.ReviewRequests
	)

	empty.Executor, empty.Pool, empty.CodeLocation = "", "", location
	empty.WriteBack, empty.ReviewRequests = write, review

	if err := empty.Validate(); err != nil {
		t.Fatalf("Validate of empty settings: %v", err)
	}

	full := validSettings(t)
	if err := full.Validate(); err != nil {
		t.Fatalf("Validate of full settings: %v", err)
	}
}

func resolveContext() unitsettings.Context {
	return unitsettings.Context{
		DefaultConnection: testConnection, HasJobPool: true, DefaultFiles: []string{"Pulumi.prod.yaml"},
	}
}

func TestResolveWithNoRecord(t *testing.T) {
	t.Parallel()

	e := unitsettings.Resolve(nil, poolDefaults(testPool), resolveContext())

	if e.Executor.Value != job.ExecutorRunner || e.Executor.Origin != unitsettings.OriginBuiltIn {
		t.Errorf("executor = %+v, want runner from the built-in default", e.Executor)
	}

	if e.Pool.Value != testPool || e.Pool.Origin != unitsettings.OriginNamespaceDefault {
		t.Errorf("pool = %+v, want the pool of the namespace default", e.Pool)
	}

	if e.Connection.Value != testConnection || e.Connection.Origin != unitsettings.OriginNamespaceDefault {
		t.Errorf("connection = %+v, want the default connection from the namespace", e.Connection)
	}

	if e.Directory.Value != "." || e.Directory.Origin != unitsettings.OriginBuiltIn {
		t.Errorf("directory = %+v, want the root from the built-in default", e.Directory)
	}
}

func TestResolveWithNoRecordGivesBuiltInWriteBack(t *testing.T) {
	t.Parallel()

	e := unitsettings.Resolve(nil, poolDefaults(testPool), resolveContext())

	if e.Delivery.Value != job.DeliveryReviewRequest || e.Delivery.Origin != unitsettings.OriginBuiltIn {
		t.Errorf("delivery = %+v, want review-request from the built-in default", e.Delivery)
	}

	if !reflect.DeepEqual(e.Files.Value, []string{"Pulumi.prod.yaml"}) || e.Files.Origin != unitsettings.OriginBuiltIn {
		t.Errorf("files = %+v, want the default file of the tool", e.Files)
	}

	if e.Repository.Value != "" || e.ReviewRequests.Value {
		t.Errorf("Resolve = %+v, want no repository and review requests off", e)
	}
}

func TestResolveRecordWinsOverDefaults(t *testing.T) {
	t.Parallel()

	s := validSettings(t)
	s.Pool = "rp-aaaaaaaaaaaaaaaa"
	s.Executor = job.ExecutorWriteBack
	s.Steps = []job.Step{planStep(nil)}

	e := unitsettings.Resolve(&s, poolDefaults(testPool), resolveContext())

	for name, f := range map[string]unitsettings.Origin{
		"executor": e.Executor.Origin, "pool": e.Pool.Origin, "connection": e.Connection.Origin,
		"repository": e.Repository.Origin, "ref": e.Ref.Origin, "directory": e.Directory.Origin,
		"delivery": e.Delivery.Origin, "files": e.Files.Origin, "review requests": e.ReviewRequests.Origin,
		"steps": e.Steps.Origin,
	} {
		if f != unitsettings.OriginUnit {
			t.Errorf("origin of %s = %q, want unit", name, f)
		}
	}

	if e.Pool.Value != s.Pool || e.Executor.Value != job.ExecutorWriteBack || e.Directory.Value != "stacks/prod" {
		t.Fatalf("Resolve = %+v, want the values of the record", e)
	}
}

func TestResolveWithNoPoolGivesWriteBack(t *testing.T) {
	t.Parallel()

	var none unitsettings.Context

	e := unitsettings.Resolve(nil, poolDefaults(""), none)
	if e.Executor.Value != job.ExecutorWriteBack || e.Executor.Origin != unitsettings.OriginBuiltIn ||
		e.Pool.Value != "" || e.Connection.Value != "" {
		t.Fatalf("Resolve = %+v, want write-back and no pool", e)
	}
}

func TestMarshalParseRoundTrip(t *testing.T) {
	t.Parallel()

	s := validSettings(t)
	step := planStep(map[string]string{"A": "b"})
	step.Dir, step.Timeout = "x", 5*time.Second
	step.Outputs = []job.Output{{Kind: job.OutputPlanDisplay, Path: "p.json"}}
	s.Steps = []job.Step{step}

	doc, err := unitsettings.Marshal(&s)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	got, err := unitsettings.Parse(doc)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}

	s.Unit = identity.Object{}
	if !reflect.DeepEqual(got, s) {
		t.Fatalf("Parse = %+v, want %+v", got, s)
	}
}

func TestParseFailsClosed(t *testing.T) {
	t.Parallel()

	for name, doc := range map[string]string{
		"unknown field":     `{"executor":"runner","extra":1}`,
		"second value":      `{} {}`,
		"not json":          `nope`,
		"invalid executor":  `{"executor":"shell"}`,
		"directory escapes": `{"codeLocation":{"directory":"../x"}}`,
		"ref is an option":  `{"codeLocation":{"ref":"-x"}}`,
		"step without args": `{"steps":[{"name":"a","role":"tool","phase":"plan","args":[]}]}`,
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			if _, err := unitsettings.Parse([]byte(doc)); !errors.Is(err, unitsettings.ErrInvalid) {
				t.Fatalf("Parse = %v, want ErrInvalid", err)
			}
		})
	}
}

func TestChangeEntryNamesOldAndNewValues(t *testing.T) {
	t.Parallel()

	old := validSettings(t)
	next := validSettings(t)
	next.CodeLocation.Ref = "release"
	next.Pool = ""

	var base audit.Entry

	e := unitsettings.ChangeEntry(base, &old, &next)
	if e.Action != unitsettings.AuditActionChanged {
		t.Fatalf("action = %q, want %q", e.Action, unitsettings.AuditActionChanged)
	}

	got := map[string]audit.Change{}
	for _, c := range e.Changes {
		got[c.Key] = c
	}

	if len(got) != len(e.Changes) || len(got) != 2 {
		t.Fatalf("changes = %+v, want one change of the pool and one of the ref", e.Changes)
	}

	if c := got["pool"]; c.Old == nil || *c.Old != testPool || c.New != "(not set)" {
		t.Errorf("pool change = %+v", c)
	}

	if c := got["codeLocation.ref"]; c.Old == nil || *c.Old != refMain || c.New != "release" {
		t.Errorf("ref change = %+v", c)
	}
}

func TestChangeEntryOfEqualSettingsHasNoChange(t *testing.T) {
	t.Parallel()

	old := validSettings(t)
	next := validSettings(t)
	next.CodeLocation.Ref = "release"

	var base audit.Entry

	if got := unitsettings.ChangeEntry(base, &old, &old); len(got.Changes) != 0 {
		t.Fatalf("equal settings gave %d changes", len(got.Changes))
	}

	if !unitsettings.Equal(&old, &old) || unitsettings.Equal(&old, &next) {
		t.Fatal("Equal does not tell equal settings from different ones")
	}
}

func TestNamespaceDefaultsValidate(t *testing.T) {
	t.Parallel()

	if err := poolDefaults(testPool).Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}

	if err := poolDefaults("x").Validate(); !errors.Is(err, unitsettings.ErrInvalid) {
		t.Fatalf("Validate = %v, want ErrInvalid", err)
	}
}
