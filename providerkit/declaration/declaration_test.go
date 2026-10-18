package declaration_test

import (
	"bytes"
	"errors"
	"os"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/providerkit/declaration"
)

func load(t *testing.T, name string) declaration.Document {
	t.Helper()

	root, err := os.OpenRoot("testdata")
	if err != nil {
		t.Fatal(err)
	}

	data, err := root.ReadFile(name)
	if err != nil {
		t.Fatal(err)
	}

	if err = root.Close(); err != nil {
		t.Fatal(err)
	}

	doc, err := declaration.Parse(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("Parse(%s) error = %v", name, err)
	}

	return doc
}

func TestExamplesParse(t *testing.T) {
	t.Parallel()

	for _, name := range []string{"ipam.json", "oncall.json"} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			load(t, name)
		})
	}
}

func TestParseRefuses(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		in   string
		want error
	}{
		{name: "no schema key", in: `{"provider": {"name": "x"}}`, want: declaration.ErrSchemaVersion},
		{name: "a later schema", in: `{"schema": 2}`, want: declaration.ErrSchemaVersion},
		{name: "not JSON", in: `schema: 1`, want: declaration.ErrInvalid},
		{
			name: "unknown key",
			in:   `{"schema": 1, "provider": {"name": "x"}, "cli": {"program": "x"}, "extra": true}`,
			want: declaration.ErrInvalid,
		},
		{
			name: "two JSON values",
			in:   `{"schema": 1, "provider": {"name": "x"}, "cli": {"program": "x"}} {}`,
			want: declaration.ErrInvalid,
		},
		{name: "no engine", in: `{"schema": 1, "provider": {"name": "x"}}`, want: declaration.ErrInvalid},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if _, err := declaration.Parse(strings.NewReader(tt.in)); !errors.Is(err, tt.want) {
				t.Fatalf("Parse() error = %v, want %v", err, tt.want)
			}
		})
	}
}

// TestValidateHTTP changes one thing in the IPAM example and expects
// ErrInvalid.
func TestValidateHTTP(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		change func(d *declaration.Document)
	}{
		{name: "provider name with a hyphen", change: func(d *declaration.Document) { d.Provider.Name = "my-ipam" }},
		{
			name:   "plain http base URL",
			change: func(d *declaration.Document) { d.HTTP.BaseURL = "http://ipam.example.org" },
		},
		{name: "token is a literal", change: func(d *declaration.Document) { d.HTTP.Auth.Token = "s3cret" }},
		{
			name:   "token is not sensitive",
			change: func(d *declaration.Document) { d.HTTP.Auth.Token = "{config.endpoint}" },
		},
		{name: "unknown auth kind", change: func(d *declaration.Document) { d.HTTP.Auth.Kind = "digest" }},
		{name: "sensitive config in a query", change: func(d *declaration.Document) {
			d.Resources[0].Read.Request.Query = map[string]string{"key": "{config.token}"}
		}},
		{name: "reference in part of a path segment", change: func(d *declaration.Document) {
			d.Resources[0].Read.Request.Path = "/allocations/a-{id}"
		}},
		{name: "reference to no attribute", change: func(d *declaration.Document) {
			d.Resources[0].Read.Request.Path = "/allocations/{name}"
		}},
		{name: "file reference in an HTTP body", change: func(d *declaration.Document) {
			d.Resources[0].Create.Request.Body["/key"] = "{file:config.token}"
		}},
		{name: "unknown method", change: func(d *declaration.Document) { d.Resources[0].Read.Request.Method = "HEAD" }},
		{name: "a command for the HTTP engine", change: func(d *declaration.Document) {
			d.Resources[0].Delete.Command = &declaration.Command{Args: nil, Stdin: nil, Output: nil, NotFound: nil}
		}},
		{name: "create gives no id", change: func(d *declaration.Document) {
			delete(d.Resources[0].Create.Request.Response, "id")
		}},
		{name: "no update and an in-place attribute", change: func(d *declaration.Document) {
			d.Resources[0].Update = nil
		}},
		{name: "two resources of one name", change: func(d *declaration.Document) {
			d.Resources = append(d.Resources, d.Resources[0])
		}},
		{name: "data source leaves a computed attribute empty", change: func(d *declaration.Document) {
			delete(d.DataSources[0].Read.Request.Response, "free")
		}},
		{
			name:   "list without match",
			change: func(d *declaration.Document) { d.DataSources[0].Read.Request.List.Match = nil },
		},
		{name: "cursor pages without a cursor", change: func(d *declaration.Document) {
			d.DataSources[0].Read.Request.List.Cursor = ""
		}},
		{name: "response pointer without a slash", change: func(d *declaration.Document) {
			d.Resources[0].Read.Request.Response["address"] = "address"
		}},
		{name: "retry of a 200", change: func(d *declaration.Document) { d.HTTP.Retry.Statuses = []int{200} }},
		{name: "timeout that is not a duration", change: func(d *declaration.Document) { d.HTTP.Timeout = "30" }},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			d := load(t, "ipam.json")
			tt.change(&d)

			err := d.Validate()
			if !errors.Is(err, declaration.ErrInvalid) {
				t.Fatalf("Validate() = %v, want ErrInvalid", err)
			}
		})
	}
}

// TestValidateCLI changes one thing in the on-call example and expects
// ErrInvalid.
func TestValidateCLI(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		change func(d *declaration.Document)
	}{
		{name: "config attribute that takes the file form of a secret", change: func(d *declaration.Document) {
			d.Provider.Config = append(d.Provider.Config, declaration.Attribute{
				Name: "api_key_file", Type: "string", Mode: "optional",
				Sensitive: false, WriteOnly: false, Replace: false, Description: "",
			})
		}},
		{name: "relative program path", change: func(d *declaration.Document) { d.CLI.Program = "bin/oncall" }},
		{name: "unclean program path", change: func(d *declaration.Document) { d.CLI.Program = "/usr/../bin/oncall" }},
		{name: "sensitive config in an argument", change: func(d *declaration.Document) {
			d.Resources[0].Create.Command.Args = append(d.Resources[0].Create.Command.Args, "--key={config.api_key}")
		}},
		{name: "sensitive config in a variable", change: func(d *declaration.Document) {
			d.CLI.Env["ONCALL_KEY"] = "{config.api_key}"
		}},
		{name: "sensitive attribute in an argument", change: func(d *declaration.Document) {
			d.DataSources[0].Read.Command.Args = []string{"lookup", "{phone}"}
		}},
		{name: "file reference on the standard input", change: func(d *declaration.Document) {
			d.Resources[0].Create.Command.Stdin["/key"] = "{file:config.api_key}"
		}},
		{name: "variable PATH", change: func(d *declaration.Document) { d.CLI.Env["PATH"] = "/opt/bin" }},
		{name: "variable of the launcher", change: func(d *declaration.Document) { d.CLI.Env["DUSTPAN_JOB"] = "x" }},
		{name: "lowercase variable", change: func(d *declaration.Document) { d.CLI.Env["oncall_team"] = "x" }},
		{name: "exit code 0 as not found", change: func(d *declaration.Document) {
			d.Resources[0].Read.Command.NotFound = []int{0}
		}},
		{name: "an HTTP request for the CLI engine", change: func(d *declaration.Document) {
			d.Resources[0].Delete.Request = &declaration.Request{
				Method:     "DELETE",
				Path:       "/x",
				Query:      nil,
				Body:       nil,
				Response:   nil,
				NotFound:   nil,
				Idempotent: false,
				List:       nil,
			}
		}},
		{name: "both engines", change: func(d *declaration.Document) {
			d.HTTP = &declaration.HTTPEngine{
				BaseURL: "https://x.example.org", Auth: declaration.Auth{
					Kind: "none", Token: "", Header: "", Username: "", Password: "", TokenURL: "", ClientID: "",
					ClientSecret: "", Scopes: nil,
				},
				Retry: declaration.Retry{MaxAttempts: 0, Statuses: nil, MaxWait: ""}, Timeout: "", OpenAPI: "",
			}
		}},
		{name: "output fills a write-only attribute", change: func(d *declaration.Document) {
			d.Resources[0].Attributes[3].WriteOnly = true
		}},
		{name: "unclosed reference", change: func(d *declaration.Document) {
			d.Resources[0].Delete.Command.Args = []string{"override", "delete", "{id"}
		}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			d := load(t, "oncall.json")
			tt.change(&d)

			err := d.Validate()
			if !errors.Is(err, declaration.ErrInvalid) {
				t.Fatalf("Validate() = %v, want ErrInvalid", err)
			}
		})
	}
}

func TestParseTemplate(t *testing.T) {
	t.Parallel()

	tests := []struct {
		in   string
		refs []string
		ok   bool
	}{
		{in: "plain", refs: nil, ok: true},
		{in: "{id}", refs: []string{"{id}"}, ok: true},
		{in: "--schedule={schedule}", refs: []string{"{schedule}"}, ok: true},
		{in: "{config.endpoint}/v1", refs: []string{"{config.endpoint}"}, ok: true},
		{in: "{file:config.api_key}", refs: []string{"{file:config.api_key}"}, ok: true},
		{in: "{a}{b}", refs: []string{"{a}", "{b}"}, ok: true},
		{in: "{}", refs: nil, ok: false},
		{in: "{Name}", refs: nil, ok: false},
		{in: "{env.HOME}", refs: nil, ok: false},
		{in: "a}b", refs: nil, ok: false},
		{in: "{id", refs: nil, ok: false},
	}

	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			t.Parallel()

			tmpl, err := declaration.ParseTemplate(tt.in)
			if !tt.ok {
				if !errors.Is(err, declaration.ErrInvalid) {
					t.Fatalf("ParseTemplate(%q) error = %v, want ErrInvalid", tt.in, err)
				}

				return
			}

			if err != nil {
				t.Fatalf("ParseTemplate(%q) error = %v", tt.in, err)
			}

			if got := refStrings(tmpl); got != strings.Join(tt.refs, " ") {
				t.Fatalf("Refs() = %v, want %v", got, tt.refs)
			}
		})
	}
}

func refStrings(t declaration.Template) string {
	refs := make([]string, 0, len(t))
	for _, r := range t.Refs() {
		refs = append(refs, r.String())
	}

	return strings.Join(refs, " ")
}

var errNoValue = errors.New("no value")

func TestExpandArg(t *testing.T) {
	t.Parallel()

	values := map[string]string{"id": "o-1", "name": "-rf", "team": "ops"}
	lookup := func(r declaration.Ref) (string, error) {
		v, ok := values[r.Name]
		if !ok {
			return "", errNoValue
		}

		return v, nil
	}

	tests := []struct {
		arg  string
		want string
		err  error
	}{
		{arg: "{id}", want: "o-1", err: nil},
		{arg: "--team={team}", want: "--team=ops", err: nil},
		{arg: "--name={name}", want: "--name=-rf", err: nil},
		{arg: "{name}", want: "", err: declaration.ErrOptionValue},
		{arg: "{name}-suffix", want: "", err: declaration.ErrOptionValue},
		{arg: "$(id) {team}", want: "$(id) ops", err: nil},
		{arg: "--x={missing}", want: "", err: errNoValue},
	}

	for _, tt := range tests {
		t.Run(tt.arg, func(t *testing.T) {
			t.Parallel()

			tmpl, err := declaration.ParseTemplate(tt.arg)
			if err != nil {
				t.Fatal(err)
			}

			got, err := tmpl.ExpandArg(lookup)
			if got != tt.want || !errors.Is(err, tt.err) {
				t.Fatalf("ExpandArg() = %q, %v, want %q, %v", got, err, tt.want, tt.err)
			}
		})
	}
}
