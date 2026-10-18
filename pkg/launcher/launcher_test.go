package launcher_test

import (
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/pkg/launcher"
	"opendustpan.org/opendustpan/pkg/runner"
)

const server = "https://dustpan.example.com"

func TestParse(t *testing.T) {
	t.Parallel()

	k8s := []string{
		"/dustpan/bin/dustpan-launcher", "run", "--server", server, "--handoff", "kubernetes",
		"--coordinator", "https://coordinator.runners.svc:8443", "--ca-file", "/dustpan/ca.pem",
		"--token-file", "/var/run/secrets/opendustpan/token",
	}

	cases := []struct {
		name string
		argv []string
		mode launcher.Mode
		ok   bool
	}{
		{"kubernetes", k8s, launcher.ModeRun, true},
		{
			"stdin",
			[]string{"dustpan-launcher", "run", "--server", server, "--handoff", "stdin"},
			launcher.ModeRun,
			true,
		},
		{
			"vsock",
			[]string{"dustpan-launcher", "run", "--server", server, "--handoff", "vsock"},
			launcher.ModeRun,
			true,
		},
		{
			"pipe with dirs",
			[]string{
				"dustpan-launcher",
				"run",
				"--server",
				server,
				"--handoff",
				"pipe",
				"--work-dir",
				"/w",
				"--secrets-dir",
				"/s",
			},
			launcher.ModeRun,
			true,
		},
		{"copy", []string{"dustpan-launcher", "copy", "--dest", "/dustpan/bin"}, launcher.ModeCopy, true},
		{"opentofu helper", []string{
			"/home/step/.terraform.d/plugins/terraform-credentials-dustpan", "get",
			"dustpan.example.com",
		}, launcher.ModeOpenTofuHelper, true},
		{"git helper", []string{"/dustpan/secrets/bin/git-credential-dustpan", "get"}, launcher.ModeGitHelper, true},
		{"no argv", nil, "", false},
		{"no mode", []string{"dustpan-launcher"}, "", false},
		{"unknown mode", []string{"dustpan-launcher", "sh", "-c", "true"}, "", false},
		{"helper mode by name", []string{"dustpan-launcher", "git-helper"}, "", false},
		{"http server", []string{
			"dustpan-launcher", "run", "--server", "http://dustpan.example.com", "--handoff",
			"stdin",
		}, "", false},
		{"server with user", []string{
			"dustpan-launcher", "run", "--server", "https://u:p@dustpan.example.com",
			"--handoff", "stdin",
		}, "", false},
		{"unknown handoff", []string{"dustpan-launcher", "run", "--server", server, "--handoff", "env"}, "", false},
		{"kubernetes without token file", k8s[:len(k8s)-2], "", false},
		{"stdin with coordinator", []string{
			"dustpan-launcher", "run", "--server", server, "--handoff", "stdin",
			"--coordinator", "https://c",
		}, "", false},
		{"unknown flag", []string{
			"dustpan-launcher", "run", "--server", server, "--handoff", "stdin", "--token",
			"x",
		}, "", false},
		{"flag twice", []string{
			"dustpan-launcher", "run", "--server", server, "--server", server, "--handoff",
			"stdin",
		}, "", false},
		{"flag with no value", []string{"dustpan-launcher", "run", "--server"}, "", false},
		{"dest for run", []string{
			"dustpan-launcher", "run", "--server", server, "--handoff", "stdin", "--dest",
			"/x",
		}, "", false},
		{"relative dest", []string{"dustpan-launcher", "copy", "--dest", "bin"}, "", false},
	}

	for _, tc := range cases {
		got, err := launcher.Parse(tc.argv)
		if tc.ok != (err == nil) || (err != nil && !errors.Is(err, launcher.ErrUsage)) || got.Mode != tc.mode {
			t.Errorf("%s: Parse = %+v, %v, want mode %q ok %v", tc.name, got, err, tc.mode, tc.ok)
		}
	}
}

func TestParseDefaultsAndHelperArgs(t *testing.T) {
	t.Parallel()

	a, err := launcher.Parse([]string{"dustpan-launcher", "run", "--server", server, "--handoff", "pipe"})
	if err != nil || a.WorkDir != launcher.DefaultWorkDir || a.SecretsDir != launcher.DefaultSecretsDir {
		t.Fatalf("Parse = %+v, %v, want the default directories", a, err)
	}

	h, err := launcher.Parse([]string{launcher.HelperOpenTofu, "get", "dustpan.example.com"})
	if err != nil || !slices.Equal(h.HelperArgs, []string{"get", "dustpan.example.com"}) {
		t.Fatalf("Parse of the helper = %+v, %v", h, err)
	}
}

func TestCommandParsesBack(t *testing.T) {
	t.Parallel()

	cases := []launcher.Args{
		{ //nolint:gosec // The token file is a path, not a credential.
			Mode: launcher.ModeRun, Server: server, Handoff: runner.HandoffKubernetes,
			Coordinator: "https://coordinator:8443", CAFile: "/dustpan/ca.pem",
			TokenFile: "/var/run/token",
			WorkDir:   launcher.DefaultWorkDir, SecretsDir: launcher.DefaultSecretsDir, Dest: "", HelperArgs: nil,
		},
		{
			Mode: launcher.ModeRun, Server: server, Handoff: runner.HandoffStdin, Coordinator: "", CAFile: "",
			TokenFile: "", WorkDir: "/work", SecretsDir: "/secrets", Dest: "", HelperArgs: nil,
		},
	}

	for _, want := range cases {
		argv := launcher.Command(launcher.DefaultBinDir, &want)
		if argv[0] != "/dustpan/bin/dustpan-launcher" {
			t.Errorf("program = %q", argv[0])
		}

		got, err := launcher.Parse(argv)
		if err != nil {
			t.Fatalf("Parse(Command) = %v for %q", err, argv)
		}

		if got.Server != want.Server || got.Handoff != want.Handoff || got.Coordinator != want.Coordinator ||
			got.CAFile != want.CAFile || got.TokenFile != want.TokenFile || got.WorkDir != want.WorkDir ||
			got.SecretsDir != want.SecretsDir {
			t.Errorf("Parse(Command) = %+v, want %+v", got, want)
		}
	}
}

func TestDispositionFor(t *testing.T) {
	t.Parallel()

	cases := []struct {
		code launcher.ExitCode
		want launcher.Disposition
	}{
		{launcher.ExitOK, launcher.DispositionNone},
		{launcher.ExitClaimLost, launcher.DispositionNone},
		{launcher.ExitUsage, launcher.DispositionRelease},
		{launcher.ExitHandoff, launcher.DispositionRelease},
		{launcher.ExitVersion, launcher.DispositionRelease},
		{launcher.ExitSandbox, launcher.DispositionRelease},
		{launcher.ExitSource, launcher.DispositionExpire},
		{launcher.ExitUnreported, launcher.DispositionExpire},
		{launcher.ExitInternal, launcher.DispositionExpire},
		{1, launcher.DispositionExpire},
		{137, launcher.DispositionExpire},
		{-1, launcher.DispositionExpire},
	}

	for _, tc := range cases {
		if got := launcher.DispositionFor(tc.code); got != tc.want {
			t.Errorf("DispositionFor(%d) = %s, want %s", tc.code, got, tc.want)
		}
	}

	for _, c := range []launcher.ExitCode{
		launcher.ExitUsage, launcher.ExitHandoff, launcher.ExitVersion, launcher.ExitSandbox, launcher.ExitSource,
		launcher.ExitClaimLost, launcher.ExitUnreported, launcher.ExitInternal,
	} {
		if c < 1 || c > 125 {
			t.Errorf("exit code %d clashes with the codes of a shell or a signal", c)
		}
	}
}

// envInput returns an input with no step and no tool variable.
func envInput() launcher.EnvInput {
	return launcher.EnvInput{
		Path: "/usr/local/bin:/usr/bin", Home: "/dustpan/work/home", Tmp: "/dustpan/work/tmp",
		Dustpan: map[string]string{launcher.EnvJob: "job-1", launcher.EnvStep: "plan"}, Step: nil, Tool: nil,
	}
}

func TestBuildEnv(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name   string
		change func(in *launcher.EnvInput)
		want   []string
		ok     bool
	}{
		{"base", func(*launcher.EnvInput) {}, []string{
			"DUSTPAN_JOB_ID=job-1", "DUSTPAN_STEP=plan", "HOME=/dustpan/work/home", "LANG=C.UTF-8",
			"PATH=/usr/local/bin:/usr/bin", "TMPDIR=/dustpan/work/tmp",
		}, true},
		{"step and tool variables", func(in *launcher.EnvInput) {
			in.Step = map[string]string{"TF_IN_AUTOMATION": "1"}
			in.Tool = map[string]string{launcher.EnvOpenTofuConfig: "/dustpan/secrets/plan/tofu.tfrc"}
		}, []string{
			"DUSTPAN_JOB_ID=job-1", "DUSTPAN_STEP=plan", "HOME=/dustpan/work/home", "LANG=C.UTF-8",
			"PATH=/usr/local/bin:/usr/bin", "TF_CLI_CONFIG_FILE=/dustpan/secrets/plan/tofu.tfrc", "TF_IN_AUTOMATION=1",
			"TMPDIR=/dustpan/work/tmp",
		}, true},
		{"step sets PATH", func(in *launcher.EnvInput) { in.Step = map[string]string{"PATH": "/tmp"} }, nil, false},
		{
			"step sets home in lower case",
			func(in *launcher.EnvInput) { in.Step = map[string]string{"home": "/"} },
			nil,
			false,
		},
		{"step uses the reserved prefix", func(in *launcher.EnvInput) {
			in.Step = map[string]string{"dustpan_job_id": "x"}
		}, nil, false},
		{"step sets a credential", func(in *launcher.EnvInput) {
			in.Step = map[string]string{"AWS_SECRET_ACCESS_KEY": "x"}
		}, nil, false},
		{"step sets a tool variable", func(in *launcher.EnvInput) {
			in.Step = map[string]string{"PULUMI_HOME": "/tmp"}
			in.Tool = map[string]string{launcher.EnvPulumiHome: "/dustpan/secrets/apply/pulumi-home"}
		}, nil, false},
		{"launcher variable without the prefix", func(in *launcher.EnvInput) {
			in.Dustpan = map[string]string{"JOB": "x"}
		}, nil, false},
		{
			"tool variable sets HOME",
			func(in *launcher.EnvInput) { in.Tool = map[string]string{"HOME": "/"} },
			nil,
			false,
		},
	}

	for _, tc := range cases {
		in := envInput()
		tc.change(&in)

		got, err := launcher.BuildEnv(in)
		if tc.ok != (err == nil) || (err != nil && !errors.Is(err, launcher.ErrEnv)) {
			t.Errorf("%s: BuildEnv = %v, want ok %v", tc.name, err, tc.ok)

			continue
		}

		if tc.ok && !slices.Equal(got, tc.want) {
			t.Errorf("%s: BuildEnv = %q, want %q", tc.name, got, tc.want)
		}
	}
}

func TestCredentialDelivery(t *testing.T) {
	t.Parallel()

	if d, err := launcher.DeliveryFor(launcher.ToolOpenTofu); err != nil || d != launcher.DeliveryHelper {
		t.Errorf("DeliveryFor(opentofu) = %s, %v", d, err)
	}

	if d, err := launcher.DeliveryFor(launcher.ToolPulumi); err != nil || d != launcher.DeliveryFile {
		t.Errorf("DeliveryFor(pulumi) = %s, %v", d, err)
	}

	if _, err := launcher.DeliveryFor("terraform"); !errors.Is(err, launcher.ErrNoCredentials) {
		t.Errorf("DeliveryFor(terraform) = %v, want ErrNoCredentials", err)
	}

	if got := string(launcher.OpenTofuConfig()); got != "credentials_helper \"dustpan\" {}\n" {
		t.Errorf("OpenTofuConfig = %q", got)
	}
}

func TestOpenTofuAnswer(t *testing.T) {
	t.Parallel()

	cases := []struct {
		host, token, want string
	}{
		{"dustpan.example.com", "tok", `{"token":"tok"}` + "\n"},
		{"DUSTPAN.example.com", "tok", `{"token":"tok"}` + "\n"},
		{"registry.opentofu.org", "tok", "{}\n"},
		{"dustpan.example.com", "", "{}\n"},
	}

	for _, tc := range cases {
		got, err := launcher.OpenTofuAnswer(tc.host, server, tc.token)
		if err != nil || string(got) != tc.want {
			t.Errorf("OpenTofuAnswer(%s) = %q, %v, want %q", tc.host, got, err, tc.want)
		}
	}

	if _, err := launcher.OpenTofuAnswer("x", "not a url", "tok"); err == nil {
		t.Error("OpenTofuAnswer with a bad server URL succeeded")
	}
}

func TestPulumiCredentials(t *testing.T) {
	t.Parallel()

	b, err := launcher.PulumiCredentials(server, "tok")
	if err != nil {
		t.Fatalf("PulumiCredentials: %v", err)
	}

	var got struct {
		Current      string            `json:"current"`
		AccessTokens map[string]string `json:"accessTokens"`
	}

	if err := json.Unmarshal(b, &got); err != nil || got.Current != server || got.AccessTokens[server] != "tok" {
		t.Fatalf("PulumiCredentials = %s, %v", b, err)
	}
}

func TestGitConfig(t *testing.T) {
	t.Parallel()

	cases := []struct{ helper, want string }{
		{"", "[credential]\n\thelper =\n"},
		{launcher.HelperGit, "[credential]\n\thelper =\n\thelper = dustpan\n"},
		{"/usr/local/bin/vault-helper", "[credential]\n\thelper =\n\thelper = /usr/local/bin/vault-helper\n"},
	}

	for _, tc := range cases {
		got := string(launcher.GitConfig(tc.helper))
		if got != tc.want || strings.Contains(got, "!") {
			t.Errorf("GitConfig(%q) = %q, want %q", tc.helper, got, tc.want)
		}
	}
}

func TestStepUserIsolated(t *testing.T) {
	t.Parallel()

	cases := []struct {
		user launcher.StepUser
		want bool
	}{
		{launcher.StepUser{UID: 0, GID: 0}, false},
		{launcher.StepUser{UID: 1000, GID: 1000}, false},
		{launcher.StepUser{UID: 2000, GID: 2000}, true},
	}

	for _, tc := range cases {
		if got := tc.user.Isolated(1000); got != tc.want {
			t.Errorf("%+v.Isolated(1000) = %v, want %v", tc.user, got, tc.want)
		}
	}
}
