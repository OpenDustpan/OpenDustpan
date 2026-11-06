package selfservice_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/internal/selfservice"
)

func readFile(t *testing.T, name string) []byte {
	t.Helper()

	data, err := fs.ReadFile(os.DirFS("testdata"), name)
	if err != nil {
		t.Fatalf("ReadFile() = %v", err)
	}

	return data
}

func parseDocument(t *testing.T, data string) selfservice.Document {
	t.Helper()

	d, err := selfservice.ParseDocument([]byte(data))
	if err != nil {
		t.Fatalf("ParseDocument() = %v", err)
	}

	return d
}

const minimalDocument = `{"schemaVersion":1,"namespace":"payments","name":"orders-db",` +
	`"blueprint":{"name":"postgres","version":3}}`

func TestParseDocument(t *testing.T) {
	t.Parallel()

	head := `{"schemaVersion":1,"namespace":"payments","name":"orders-db","blueprint":{"name":"postgres","version":3}`

	cases := []struct {
		name string
		data string
		want error
	}{
		{"the document of the ADR", string(readFile(t, "orders-db.request.json")), nil},
		{"the smallest document", minimalDocument, nil},
		{"absent", head + `,"ensure":"absent"}`, nil},
		{"a list of strings", head + `,"inputs":{"zones":["a","b"]}}`, nil},
		{"a home in OpenDustpan", head + `,"home":{"kind":"opendustpan"}}`, nil},
		{
			"a newer schema version", strings.Replace(minimalDocument, `"schemaVersion":1`, `"schemaVersion":2`, 1),
			selfservice.ErrSchemaVersion,
		},
		{
			"no schema version",
			`{"namespace":"payments","name":"orders-db","blueprint":{"name":"postgres","version":3}}`,
			selfservice.ErrInvalid,
		},
		{"an unknown field", head + `,"comment":"x"}`, selfservice.ErrInvalid},
		{"a duplicate key", head + `,"inputs":{"size":"small","size":"large"}}`, selfservice.ErrInvalid},
		{"a duplicate key at the top", head + `,"name":"other"}`, selfservice.ErrInvalid},
		{"a string version", strings.Replace(minimalDocument, `"version":3`, `"version":"3"`, 1), selfservice.ErrInvalid},
		{"version 0", strings.Replace(minimalDocument, `"version":3`, `"version":0`, 1), selfservice.ErrInvalid},
		{"a name with a capital", strings.Replace(minimalDocument, "orders-db", "Orders", 1), selfservice.ErrInvalid},
		{"a null answer", head + `,"inputs":{"size":null}}`, selfservice.ErrInvalid},
		{"an object answer", head + `,"inputs":{"size":{"a":1}}}`, selfservice.ErrInvalid},
		{"a list of numbers", head + `,"inputs":{"zones":[1,2]}}`, selfservice.ErrInvalid},
		{"a bad input name", head + `,"inputs":{"1size":"small"}}`, selfservice.ErrInvalid},
		{"a bad ensure", head + `,"ensure":"maybe"}`, selfservice.ErrInvalid},
		{
			"a home in git with no ref",
			head + `,"home":{"kind":"git","repository":"payments/infra"}}`,
			selfservice.ErrInvalid,
		},
		{
			"a home in OpenDustpan with a repository", head + `,"home":{"kind":"opendustpan","repository":"a/b"}}`,
			selfservice.ErrInvalid,
		},
		{
			"a bad delivery", head + `,"home":{"kind":"git","repository":"a/b","ref":"main","delivery":"email"}}`,
			selfservice.ErrInvalid,
		},
		{"data after the document", minimalDocument + ` {}`, selfservice.ErrInvalid},
		{
			"a long string answer",
			head + `,"inputs":{"note":"` + strings.Repeat("x", selfservice.MaxStringAnswer+1) + `"}}`,
			selfservice.ErrLimit,
		},
		{"too large", minimalDocument + strings.Repeat(" ", selfservice.MaxDocumentBytes), selfservice.ErrLimit},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			_, err := selfservice.ParseDocument([]byte(tc.data))
			if !errors.Is(err, tc.want) || (tc.want == nil && err != nil) {
				t.Fatalf("ParseDocument() = %v, want %v", err, tc.want)
			}
		})
	}
}

func TestCanonical(t *testing.T) {
	t.Parallel()

	b := parseBlueprint(t, readFile(t, "postgres.blueprint.json"))

	a := parseDocument(t, `{"schemaVersion":1,"namespace":"payments","name":"orders-db",`+
		`"blueprint":{"name":"postgres","version":3},`+
		`"inputs":{"subnetGroup":"orders-private","network":"net-0a1b2c"},`+
		`"home":{"kind":"opendustpan"}}`)
	// The same state: other key order, white space, the defaults written
	// out, a number in another form, a string with an escape, ensure
	// written out, and another home, which a later submit ignores.
	c := parseDocument(t, `{"inputs":{"network":"net-0a1b2c","size":"sm\u0061ll","storageGb":2.0e1,`+
		`"subnetGroup":"orders-private"},"ensure":"present",`+
		`"blueprint":{"version":3,"name":"postgres"},"name":"orders-db","namespace":"payments",`+
		`"schemaVersion":1, "home":{"kind":"git","repository":"a/b","ref":"main"}}`)

	ca, err := a.Canonical(&b)
	if err != nil {
		t.Fatalf("Canonical() = %v", err)
	}

	cc, err := c.Canonical(&b)
	if err != nil {
		t.Fatalf("Canonical() = %v", err)
	}

	if !bytes.Equal(ca, cc) {
		t.Fatalf("Canonical() differs:\n%s\n%s", ca, cc)
	}

	want := `{"blueprint":{"name":"postgres","version":3},"ensure":"present",` +
		`"inputs":{"network":"net-0a1b2c","size":"small","storageGb":20,"subnetGroup":"orders-private"},` +
		`"name":"orders-db","namespace":"payments","schemaVersion":1}`
	if string(ca) != want {
		t.Fatalf("Canonical() = %s, want %s", ca, want)
	}

	other := parseDocument(t, strings.Replace(minimalDocument, `}}`, `},"inputs":{"size":"large"}}`, 1))

	co, err := other.Canonical(&b)
	if err != nil || bytes.Equal(co, ca) {
		t.Fatalf("Canonical() of another answer = %s, %v", co, err)
	}
}

func TestEncode(t *testing.T) {
	t.Parallel()

	d := parseDocument(t, `{"inputs":{"size":"small","network":"net-0a1b2c"},"name":"orders-db",`+
		`"blueprint":{"version":3,"name":"postgres"},"namespace":"payments","schemaVersion":1,`+
		`"home":{"kind":"git","repository":"payments/infra","ref":"main"}}`)

	got, err := d.Encode()
	if err != nil {
		t.Fatalf("Encode() = %v", err)
	}

	want := `{
  "schemaVersion": 1,
  "namespace": "payments",
  "name": "orders-db",
  "blueprint": {
    "name": "postgres",
    "version": 3
  },
  "inputs": {
    "network": "net-0a1b2c",
    "size": "small"
  },
  "home": {
    "kind": "git",
    "repository": "payments/infra",
    "ref": "main"
  }
}
`
	if string(got) != want {
		t.Fatalf("Encode() =\n%s\nwant\n%s", got, want)
	}

	back := parseDocument(t, string(got))
	if !json.Valid(got) || back.Name != d.Name || len(back.Inputs) != len(d.Inputs) {
		t.Fatalf("ParseDocument(Encode()) = %+v", back)
	}
}

func TestOperationFor(t *testing.T) {
	t.Parallel()

	cases := []struct {
		last, ensure selfservice.Ensure
		want         selfservice.Operation
		err          error
	}{
		{last: "", ensure: selfservice.EnsurePresent, want: selfservice.OperationCreate, err: nil},
		{last: "", ensure: "", want: selfservice.OperationCreate, err: nil},
		{last: selfservice.EnsurePresent, ensure: selfservice.EnsurePresent, want: selfservice.OperationUpdate, err: nil},
		{last: selfservice.EnsurePresent, ensure: selfservice.EnsureAbsent, want: selfservice.OperationDestroy, err: nil},
		{last: "", ensure: selfservice.EnsureAbsent, want: "", err: selfservice.ErrInvalid},
		{last: selfservice.EnsureAbsent, ensure: selfservice.EnsureAbsent, want: selfservice.OperationNone, err: nil},
		{last: selfservice.EnsureAbsent, ensure: selfservice.EnsurePresent, want: selfservice.OperationCreate, err: nil},
		{last: "", ensure: "maybe", want: "", err: selfservice.ErrInvalid},
	}

	for _, tc := range cases {
		got, err := selfservice.OperationFor(tc.last, tc.ensure)
		if got != tc.want || !errors.Is(err, tc.err) || (tc.err == nil && err != nil) {
			t.Fatalf("OperationFor(%q, %q) = %q, %v, want %q, %v", tc.last, tc.ensure, got, err, tc.want, tc.err)
		}
	}
}

func TestValidNames(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name         string
		valid, input bool
	}{
		{name: "orders-db", valid: true, input: false},
		{name: "a", valid: true, input: true},
		{name: strings.Repeat("a", selfservice.MaxNameBytes), valid: true, input: true},
		{name: strings.Repeat("a", selfservice.MaxNameBytes+1), valid: false, input: true},
		{name: "subnetGroup", valid: false, input: true},
		{name: "storage_gb", valid: false, input: true},
		{name: "1db", valid: false, input: false},
		{name: "-db", valid: false, input: false},
		{name: "", valid: false, input: false},
		{name: "db.prod", valid: false, input: false},
	} {
		if got := selfservice.ValidName(tc.name); got != tc.valid {
			t.Fatalf("ValidName(%q) = %v, want %v", tc.name, got, tc.valid)
		}

		if got := selfservice.ValidInputName(tc.name); got != tc.input {
			t.Fatalf("ValidInputName(%q) = %v, want %v", tc.name, got, tc.input)
		}
	}
}
