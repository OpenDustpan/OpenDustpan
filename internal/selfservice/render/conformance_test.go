package render_test

import (
	"slices"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/internal/selfservice"
	"opendustpan.org/opendustpan/internal/selfservice/render"
	"opendustpan.org/opendustpan/internal/selfservice/selfservicetest"
	"opendustpan.org/opendustpan/pkg/identity"
)

func TestTofuConformance(t *testing.T) {
	t.Parallel()

	base := tofuInput(t)

	selfservicetest.RunRenderer(t, selfservicetest.RendererSuite{
		Renderer: render.NewTofu(),
		Tool:     selfservice.ToolOpenTofu,
		Cases: operationCases(base, unit(t, selfservice.ToolOpenTofu, "orders-db", "unit-1"),
			[]string{"backup_retention_days", "instance_size", "network_id", "storage_gb"},
			map[string]string{
				"main.tf":               golden(t, "tofu.main.tf"),
				"terraform.tfvars.json": golden(t, "tofu.terraform.tfvars.json"),
			}),
		Failures: slices.Concat(
			sharedFailures(base, "source", "count"),
			tofuFailures(
				unit(t, selfservice.ToolPulumi, "orders-db", ""), identity.NamespaceObject(testNamespace(t)), base,
			),
			textFailures(base, "module version", func(in *selfservice.RenderInput, s string) {
				m := *in.Version.Blueprint.Program.Module
				m.Version = s
				in.Version.Blueprint.Program.Module = &m
			}),
			textFailures(base, "module source", func(in *selfservice.RenderInput, s string) {
				m := *in.Version.Blueprint.Program.Module
				m.Source = s
				in.Version.Blueprint.Program.Module = &m
			},
				"../../../../etc/x", "../x", "./x", "/abs/x", "git::https://example.com/r.git?ref=main",
				"git::https://example.com/r.git", "s3::https://bucket.example.com/m.zip", "gcs::https://example.com/m",
				"http://example.com/m.zip", "https://example.com/m.zip", "github.com/x/y", "a/b", "a/b/c/d/e",
				"ns/name/sys?ref=x", "ns/name/sys#x", "ns@x/name/sys", "ns/../sys", "host/ns/name/sys",
				"reg.example.com/../a/b", "reg.example.com:443/ns/name/sys",
			),
		),
	})
}

func TestPulumiConformance(t *testing.T) {
	t.Parallel()

	base := pulumiInput(t)
	component := func(set func(*selfservice.Component, string)) func(*selfservice.RenderInput, string) {
		return func(in *selfservice.RenderInput, s string) {
			c := *in.Version.Blueprint.Program.Component
			set(&c, s)
			in.Version.Blueprint.Program.Component = &c
		}
	}

	selfservicetest.RunRenderer(t, selfservicetest.RendererSuite{
		Renderer: render.NewPulumi(),
		Tool:     selfservice.ToolPulumi,
		Cases: operationCases(base, unit(t, selfservice.ToolPulumi, "orders-site", "unit-1"),
			[]string{"aws:region", "replicas", "size", "tags"},
			map[string]string{
				"Pulumi.yaml":             golden(t, "pulumi.Pulumi.yaml"),
				"Pulumi.orders-site.yaml": golden(t, "pulumi.Pulumi.orders-site.yaml"),
			}),
		Failures: slices.Concat(
			sharedFailures(base, "a:b:c", ":a", "a:"),
			pulumiFailures(
				unit(t, selfservice.ToolOpenTofu, "orders-site", ""), identity.NamespaceObject(testNamespace(t)), base,
			),
			textFailures(base, "version", component(func(c *selfservice.Component, s string) { c.Version = s })),
			textFailures(base, "package", component(func(c *selfservice.Component, s string) { c.Package = s })),
			textFailures(base, "type", component(func(c *selfservice.Component, s string) { c.Type = s })),
		),
	})
}

// operationCases returns the cases of the operation of a render: a create,
// an update for a unit with an ID, and a destroy.
func operationCases(
	base selfservice.RenderInput, withID identity.Object, keys []string, files map[string]string,
) []selfservicetest.RenderCase {
	setUnit := func(in *selfservice.RenderInput) { in.Unit = withID }
	setAbsent := func(in *selfservice.RenderInput) { in.Document.Ensure = selfservice.EnsureAbsent }

	return []selfservicetest.RenderCase{
		{Name: "create", Input: base, Operation: selfservice.OperationCreate, Keys: keys, Files: files},
		{
			Name: "update with a unit ID", Input: variant(base, setUnit),
			Operation: selfservice.OperationUpdate, Keys: keys, Files: files,
		},
		{
			Name: "destroy", Input: variant(base, setAbsent),
			Operation: selfservice.OperationDestroy, Keys: keys, Files: files,
		},
		{
			Name: "destroy wins over a unit ID",
			Input: variant(base, func(in *selfservice.RenderInput) {
				setAbsent(in)
				setUnit(in)
			}),
			Operation: selfservice.OperationDestroy, Keys: keys, Files: files,
		},
		{
			Name:      "empty ensure is present",
			Input:     variant(base, func(in *selfservice.RenderInput) { in.Document.Ensure = "" }),
			Operation: selfservice.OperationCreate, Keys: keys, Files: files,
		},
	}
}

// noProgram returns a program that names no module and no component.
func noProgram() selfservice.Program {
	return selfservice.Program{Module: nil, Component: nil}
}

// sharedFailures returns the failures that both tools share: the boundary
// layer for hostile keys, and the fail-closed layer for a program and a
// tool that no render can use. extraKeys are keys that only this tool
// refuses.
func sharedFailures(base selfservice.RenderInput, extraKeys ...string) []selfservicetest.RenderFailure {
	hostileKeys := append([]string{
		`a"; resource "x" "y" {}`, "${evil}", "a b", "a\nb", "a.b", "9a", "", "a=b", "a}", "a\"b",
		strings.Repeat("a", selfservice.MaxKeyBytes+1),
	}, extraKeys...)

	out := make([]selfservicetest.RenderFailure, 0, 2*len(hostileKeys)+5)

	for _, key := range hostileKeys {
		out = append(out,
			selfservicetest.RenderFailure{
				Name: "boundary: input key " + key, Err: selfservice.ErrInvalid,
				Input: variant(base, func(in *selfservice.RenderInput) { in.Version.Blueprint.Inputs[0].Maps = key }),
			},
			selfservicetest.RenderFailure{
				Name: "boundary: platform key " + key,
				Err:  selfservice.ErrInvalid,
				Input: variant(
					base,
					func(in *selfservice.RenderInput) { in.Version.Blueprint.Platform.Values[key] = raw(`1`) },
				),
			},
		)
	}

	return append(out,
		selfservicetest.RenderFailure{
			Name: "unknown tool", Err: selfservice.ErrInvalid,
			Input: variant(base, func(in *selfservice.RenderInput) { in.Version.Blueprint.Tool = "terraform" }),
		},
		selfservicetest.RenderFailure{
			Name: "no version", Err: selfservice.ErrInvalid,
			Input: variant(base, func(in *selfservice.RenderInput) { in.Version = nil }),
		},
		selfservicetest.RenderFailure{
			Name: "no document", Err: selfservice.ErrInvalid,
			Input: variant(base, func(in *selfservice.RenderInput) { in.Document = nil }),
		},
		selfservicetest.RenderFailure{
			Name: "no program",
			Err:  selfservice.ErrInvalid,
			Input: variant(
				base,
				func(in *selfservice.RenderInput) { in.Version.Blueprint.Program = noProgram() },
			),
		},
		selfservicetest.RenderFailure{
			Name: "answer for no input", Err: selfservice.ErrInvalid,
			Input: variant(base, func(in *selfservice.RenderInput) { in.Document.Inputs["unknown"] = raw(`"x"`) }),
		},
	)
}

// textFailures returns the boundary failures for the program text that set
// writes into the input: a value that would end a string or start a
// template or a YAML construct.
func textFailures(
	base selfservice.RenderInput, what string, set func(*selfservice.RenderInput, string), more ...string,
) []selfservicetest.RenderFailure {
	texts := append([]string{
		"1.0\"\n}\nresource \"x\" \"y\" {}", "${x}", "%{if true}", "a b", "", "a\\b", "!!str", "a: b",
	}, more...)

	out := make([]selfservicetest.RenderFailure, 0, len(texts))

	for _, text := range texts {
		out = append(out, selfservicetest.RenderFailure{
			Name: "boundary: " + what + " " + text, Err: selfservice.ErrInvalid,
			Input: variant(base, func(in *selfservice.RenderInput) { set(in, text) }),
		})
	}

	return out
}

func tofuFailures(otherUnit, notUnit identity.Object, base selfservice.RenderInput) []selfservicetest.RenderFailure {
	otherProgram := func(p selfservice.Program) func(*selfservice.RenderInput) {
		return func(in *selfservice.RenderInput) { in.Version.Blueprint.Program = p }
	}
	component := &selfservice.Component{Package: "p", Version: "1.0.0", Type: "p:index:T"}

	return []selfservicetest.RenderFailure{
		{
			Name: "unit of another tool", Err: selfservice.ErrInvalid,
			Input: variant(base, func(in *selfservice.RenderInput) { in.Unit = otherUnit }),
		},
		{
			Name: "no unit", Err: selfservice.ErrInvalid,
			Input: variant(base, func(in *selfservice.RenderInput) { in.Unit = identity.Object{} }),
		},
		{
			Name: "unit that is a namespace", Err: selfservice.ErrInvalid,
			Input: variant(base, func(in *selfservice.RenderInput) { in.Unit = notUnit }),
		},
		{
			Name: "one home: input and platform value",
			Err:  selfservice.ErrOneHome,
			Input: variant(
				base,
				func(in *selfservice.RenderInput) { in.Version.Blueprint.Platform.Values["instance_size"] = raw(`"x"`) },
			),
		},
		{
			Name: "one home: keys that differ in case",
			Err:  selfservice.ErrOneHome,
			Input: variant(
				base,
				func(in *selfservice.RenderInput) { in.Version.Blueprint.Platform.Values["Instance_Size"] = raw(`"x"`) },
			),
		},
		{
			Name: "pulumi blueprint",
			Err:  selfservice.ErrInvalid,
			Input: variant(
				base,
				func(in *selfservice.RenderInput) { in.Version.Blueprint.Tool = selfservice.ToolPulumi },
			),
		},
		{
			Name: "module and component",
			Err:  selfservice.ErrInvalid,
			Input: variant(
				base,
				func(in *selfservice.RenderInput) { in.Version.Blueprint.Program.Component = component },
			),
		},
		{
			Name: "component only", Err: selfservice.ErrInvalid,
			Input: variant(base, otherProgram(selfservice.Program{Module: nil, Component: component})),
		},
		{
			Name: "module from git", Err: selfservice.ErrInvalid,
			Input: variant(base, otherProgram(selfservice.Program{
				Module: &selfservice.Module{
					Source:     "",
					Version:    "",
					Connection: "",
					Repository: "payments/infra",
					Directory:  "",
					Revision:   "v1",
				},
				Component: nil,
			})),
		},
		{
			Name: "null platform value", Err: selfservice.ErrInvalid,
			Input: variant(base, func(in *selfservice.RenderInput) {
				in.Version.Blueprint.Platform.Values["backup_retention_days"] = raw(`null`)
			}),
		},
	}
}

func pulumiFailures(otherUnit, notUnit identity.Object, base selfservice.RenderInput) []selfservicetest.RenderFailure {
	otherProgram := func(p selfservice.Program) func(*selfservice.RenderInput) {
		return func(in *selfservice.RenderInput) { in.Version.Blueprint.Program = p }
	}
	module := &selfservice.Module{
		Source: "s", Version: "1.0.0", Connection: "", Repository: "", Directory: "", Revision: "",
	}
	setPlatform := func(key, value string) func(*selfservice.RenderInput) {
		return func(in *selfservice.RenderInput) { in.Version.Blueprint.Platform.Values[key] = raw(value) }
	}

	return []selfservicetest.RenderFailure{
		{
			Name:  "one home: input and platform value",
			Err:   selfservice.ErrOneHome,
			Input: variant(base, setPlatform("size", `"x"`)),
		},
		{
			Name: "one home: project namespace and no namespace", Err: selfservice.ErrOneHome,
			Input: variant(base, setPlatform("webapp:size", `"x"`)),
		},
		{
			Name:  "one home: keys that differ in case",
			Err:   selfservice.ErrOneHome,
			Input: variant(base, setPlatform("Size", `"x"`)),
		},
		{
			Name: "opentofu blueprint",
			Err:  selfservice.ErrInvalid,
			Input: variant(
				base,
				func(in *selfservice.RenderInput) { in.Version.Blueprint.Tool = selfservice.ToolOpenTofu },
			),
		},
		{
			Name: "component and module", Err: selfservice.ErrInvalid,
			Input: variant(base, func(in *selfservice.RenderInput) { in.Version.Blueprint.Program.Module = module }),
		},
		{
			Name:  "module only",
			Err:   selfservice.ErrInvalid,
			Input: variant(base, otherProgram(selfservice.Program{Module: module, Component: nil})),
		},
		{
			Name: "unit of another tool", Err: selfservice.ErrInvalid,
			Input: variant(base, func(in *selfservice.RenderInput) { in.Unit = otherUnit }),
		},
		{
			Name: "no unit", Err: selfservice.ErrInvalid,
			Input: variant(base, func(in *selfservice.RenderInput) { in.Unit = identity.Object{} }),
		},
		{
			Name: "unit that is a namespace", Err: selfservice.ErrInvalid,
			Input: variant(base, func(in *selfservice.RenderInput) { in.Unit = notUnit }),
		},
		{
			Name:  "null platform value",
			Err:   selfservice.ErrInvalid,
			Input: variant(base, setPlatform("aws:region", `null`)),
		},
		{
			Name: "encoding: noncharacter in an answer", Err: selfservice.ErrInvalid,
			Input: variant(base, func(in *selfservice.RenderInput) { in.Document.Inputs["size"] = raw(`"a￾b"`) }),
		},
		{
			Name: "encoding: noncharacter in a platform value", Err: selfservice.ErrInvalid,
			Input: variant(base, setPlatform("aws:region", `"￿"`)),
		},
	}
}
