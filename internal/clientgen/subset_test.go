package clientgen_test

import (
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"slices"
	"strings"
	"testing"
)

// descriptionPath is the published description of the project. The generator
// reads these bytes only, so this test reads the file and imports no server
// package.
const descriptionPath = "../api/apidesc/openapi.json"

// maxDescriptionBytes is the size limit of the parser (8 MiB).
const maxDescriptionBytes = 8388608

// rule is one row of the key allowlist of the subset table in RFC 99. It
// lists the keys of an object, or the shape of a map or a list.
type rule struct {
	// keys maps an allowed key to the rule of its value. An empty rule name
	// means that the value is a leaf that the parser reads as it is.
	keys map[string]string
	// mapOf is the rule of each value of a map whose keys are names.
	mapOf string
	// names limits the keys of a map, when it is not nil.
	names *regexp.Regexp
	// listOf is the rule of each item of a list.
	listOf string
	// values limits each item of a list to a string that matches the pattern,
	// when it is not nil.
	values *regexp.Regexp
}

func object(keys map[string]string) rule {
	return rule{keys: keys, mapOf: "", names: nil, listOf: "", values: nil}
}

func mapOfRule(next string, names *regexp.Regexp) rule {
	return rule{keys: nil, mapOf: next, names: names, listOf: "", values: nil}
}

func listOfRule(next string) rule {
	return rule{keys: nil, mapOf: "", names: nil, listOf: next, values: nil}
}

func enumOfRule(values *regexp.Regexp) rule {
	return rule{keys: nil, mapOf: "", names: nil, listOf: "", values: values}
}

// subsetRules is the allowlist of the subset table, by rule name.
func subsetRules() map[string]rule {
	return map[string]rule{
		"document": object(map[string]string{
			"openapi": "", "info": "info", "servers": "servers",
			"tags": "tags", "paths": "paths", "components": "components",
			"security": "security",
		}),
		"info":     object(map[string]string{"title": "", "version": "", "description": ""}),
		"servers":  listOfRule("server"),
		"server":   object(map[string]string{"url": "", "description": ""}),
		"tags":     listOfRule("tag"),
		"tag":      object(map[string]string{"name": "", "description": ""}),
		"security": listOfRule("securityRequirement"),
		// A requirement maps the name of a scheme to a list of scopes.
		"securityRequirement": mapOfRule("", nil),
		"paths":               mapOfRule("pathItem", nil),
		"pathItem": object(map[string]string{
			"get": "operation", "put": "operation", "post": "operation",
			"patch": "operation", "delete": "operation",
		}),
		"operation": object(map[string]string{
			"operationId": "", "tags": "", "summary": "", "description": "",
			"parameters": "parameters", "requestBody": "requestBody",
			"responses": "responses", "security": "security",
			"x-dustpan-actions": "",
		}),
		"parameters": listOfRule("parameter"),
		"parameter": object(map[string]string{
			"$ref": "", "name": "", "in": "", "required": "",
			"description": "", "schema": "schema",
		}),
		"requestBody": object(map[string]string{"content": "content", "required": ""}),
		"content": mapOfRule("mediaType",
			regexp.MustCompile(`^application/(json|merge-patch\+json)$`)),
		"mediaType": object(map[string]string{"schema": "schema"}),
		"responses": mapOfRule("response",
			regexp.MustCompile(`^([1-5]\d{2}|[1-5]XX|default)$`)),
		"response": object(map[string]string{
			"description": "", "content": "content", "headers": "headers",
		}),
		"headers": mapOfRule("header", nil),
		"header": object(map[string]string{
			"$ref": "", "description": "", "schema": "schema",
		}),
		"components": object(map[string]string{
			"schemas": "schemas", "parameters": "componentParameters",
			"headers": "headers", "securitySchemes": "securitySchemes",
		}),
		"schemas":             mapOfRule("schema", nil),
		"componentParameters": mapOfRule("parameter", nil),
		"securitySchemes":     mapOfRule("securityScheme", nil),
		// The schemes in the subset are `type: http` with `scheme: bearer`, and
		// `type: apiKey` in a cookie or a header, with its name.
		"securityScheme": object(map[string]string{
			"type": "", "scheme": "", "description": "", "in": "", "name": "",
		}),
		"schema": object(map[string]string{
			"$ref": "", "type": "", "properties": "properties",
			"required": "", "additionalProperties": "additionalProperties",
			"items": "schema", "enum": "enumValues", "format": "", "description": "",
			"minLength": "", "maxLength": "", "anyOf": "schemaList",
			"oneOf": "schemaList", "discriminator": "discriminator",
		}),
		"enumValues": enumOfRule(enumValuePattern()),
		// A discriminator names the member and maps its values to schemas.
		"discriminator": object(map[string]string{"propertyName": "", "mapping": "mapping"}),
		// The mapping has free keys, the values of the member, and string values.
		"mapping":    mapOfRule("", nil),
		"properties": mapOfRule("schema", nil),
		"schemaList": listOfRule("schema"),
	}
}

// walker checks the nodes of a document against the rules and collects the
// JSON pointer of each construct outside the allowlist.
type walker struct {
	rules map[string]rule
	bad   []string
}

func (w *walker) report(ptr, msg string) {
	w.bad = append(w.bad, ptr+": "+msg)
}

// walk checks node against the rule named ctx. An empty ctx is a leaf.
func (w *walker) walk(ctx string, node any, ptr string) {
	switch ctx {
	case "":
		return
	case "additionalProperties":
		w.walkAdditional(node, ptr)

		return
	}

	r, ok := w.rules[ctx]
	if !ok {
		w.report(ptr, "no rule "+ctx)

		return
	}

	switch v := node.(type) {
	case map[string]any:
		w.walkObject(ctx, r, v, ptr)
	case []any:
		w.walkList(ctx, r, v, ptr)
	default:
		w.report(ptr, "expected an object or a list")
	}
}

// walkAdditional allows `true` and a schema object. The value `false` is
// not in the subset.
func (w *walker) walkAdditional(node any, ptr string) {
	if _, ok := node.(map[string]any); ok {
		w.walk("schema", node, ptr)

		return
	}

	if b, ok := node.(bool); !ok || !b {
		w.report(ptr, "only true or a schema is in the subset")
	}
}

// hasFreeKeys says that the rule is a map with free keys and leaf values.
func hasFreeKeys(ctx string) bool {
	return ctx == "securityRequirement" || ctx == "mapping"
}

func (w *walker) walkObject(ctx string, r rule, v map[string]any, ptr string) {
	for k, child := range v {
		p := ptr + "/" + strings.ReplaceAll(strings.ReplaceAll(k, "~", "~0"), "/", "~1")

		if r.keys != nil {
			next, allowed := r.keys[k]
			if !allowed {
				w.report(p, "key is not in the subset")

				continue
			}

			w.walk(next, child, p)

			continue
		}

		if r.names != nil && !r.names.MatchString(k) {
			w.report(p, "name is not in the subset")

			continue
		}

		if r.listOf != "" || (r.mapOf == "" && !hasFreeKeys(ctx)) {
			w.report(p, "key is not in the subset")

			continue
		}

		w.walk(r.mapOf, child, p)
	}
}

func (w *walker) walkList(ctx string, r rule, v []any, ptr string) {
	if r.values != nil {
		for i, child := range v {
			s, ok := child.(string)
			if !ok || !r.values.MatchString(s) {
				w.report(fmt.Sprintf("%s/%d", ptr, i), "enum value is not in the subset")
			}
		}

		return
	}

	if r.listOf == "" && ctx != "securityRequirement" {
		w.report(ptr, "a list is not in the subset here")

		return
	}

	for i, child := range v {
		w.walk(r.listOf, child, fmt.Sprintf("%s/%d", ptr, i))
	}
}

func checkDocument(t *testing.T, raw []byte) []string {
	t.Helper()

	var doc any
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("description is not JSON: %v", err)
	}

	w := &walker{rules: subsetRules(), bad: nil}
	w.walk("document", doc, "")
	slices.Sort(w.bad)

	return w.bad
}

// TestRealDescriptionIsInSubset walks the published description and checks
// that every key path is in the allowlist of the subset table. The parser
// has no implementation yet, so this test is the executable form of the
// table: a key in the description that the table lacks fails here.
func TestRealDescriptionIsInSubset(t *testing.T) {
	t.Parallel()

	raw, err := os.ReadFile(descriptionPath)
	if err != nil {
		t.Fatalf("read description: %v", err)
	}

	if len(raw) > maxDescriptionBytes {
		t.Fatalf("description is %d bytes, over the limit of %d", len(raw), maxDescriptionBytes)
	}

	var head struct {
		OpenAPI string `json:"openapi"`
	}
	if err := json.Unmarshal(raw, &head); err != nil {
		t.Fatalf("description is not JSON: %v", err)
	}

	if head.OpenAPI != "3.1.0" {
		t.Errorf("openapi = %q, want 3.1.0", head.OpenAPI)
	}

	for _, b := range checkDocument(t, raw) {
		t.Error(b)
	}
}

// TestSubsetRefusesOtherConstructs checks that the walk reports a construct
// outside the table, so that the test above can fail.
func TestSubsetRefusesOtherConstructs(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		doc  string
		want string
	}{
		{"webhooks", `{"webhooks":{}}`, "/webhooks: key"},
		{"callbacks", `{"paths":{"/a":{"get":{"callbacks":{}}}}}`, "/paths/~1a/get/callbacks: key"},
		{"cookie parameter key", `{"paths":{"/a":{"get":{"parameters":[{"style":"form"}]}}}}`, "/style: key"},
		{"media type", `{"paths":{"/a":{"post":{"requestBody":{"content":{"text/xml":{}}}}}}}`, "text~1xml: name"},
		{"status", `{"paths":{"/a":{"get":{"responses":{"20":{}}}}}}`, "/20: name"},
		{
			"additionalProperties false",
			`{"components":{"schemas":{"A":{"additionalProperties":false}}}}`,
			"only true or a schema",
		},
		{"allOf", `{"components":{"schemas":{"A":{"allOf":[]}}}}`, "/allOf: key"},
		{"trace", `{"paths":{"/a":{"trace":{}}}}`, "/trace: key"},
		{
			"discriminator key",
			`{"components":{"schemas":{"A":{"discriminator":{"x":1}}}}}`,
			"/discriminator/x: key",
		},
		{
			"discriminator mapping as a list",
			`{"components":{"schemas":{"A":{"discriminator":{"mapping":[]}}}}}`,
			"a list is not in the subset",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			bad := checkDocument(t, []byte(tt.doc))
			if !strings.Contains(strings.Join(bad, "\n"), tt.want) {
				t.Errorf("walk = %q, want a report with %q", bad, tt.want)
			}
		})
	}
}

// TestSubsetEnumValues checks the enum allowlist of the walk: a value in the
// allowlist passes, and a value outside it is reported at its JSON pointer,
// also in a nested schema.
func TestSubsetEnumValues(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		doc  string
		want []string
	}{
		{
			"the values of the allowlist pass",
			`{"components":{"schemas":{"A":{"type":"string","enum":["onCall","awaiting-approval","lock.locked"]}}}}`,
			nil,
		},
		{
			"a value outside the allowlist",
			`{"components":{"schemas":{"A":{"type":"string","enum":["Bad"]}}}}`,
			[]string{"/components/schemas/A/enum/0: enum value is not in the subset"},
		},
		{
			"a value outside the allowlist in a nested schema",
			`{"components":{"schemas":{"A":{"properties":{"b":{"type":"string","enum":["a b"]}}}}}}`,
			[]string{"/components/schemas/A/properties/b/enum/0: enum value is not in the subset"},
		},
		{
			"a value that is not a string",
			`{"components":{"schemas":{"A":{"type":"string","enum":[1]}}}}`,
			[]string{"/components/schemas/A/enum/0: enum value is not in the subset"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if bad := checkDocument(t, []byte(tt.doc)); !slices.Equal(bad, tt.want) {
				t.Errorf("walk = %q, want %q", bad, tt.want)
			}
		})
	}
}

// enumValuePattern is the allowlist of an enum value in RFC 99. Wire values are
// camelCase (ADR 210), and some values hold a separator.
func enumValuePattern() *regexp.Regexp {
	return regexp.MustCompile(`^[a-z][a-zA-Z0-9]*([._:-][a-zA-Z0-9]+)*$`)
}

// TestEnumValueAllowlist checks the allowlist against wire values that the
// server code uses today. The list comes from the string constants and the
// JSON names in pkg/objectcontext, pkg/gate, pkg/job, pkg/drift, pkg/trigger
// and internal/api.
func TestEnumValueAllowlist(t *testing.T) {
	t.Parallel()

	wireValues := []string{
		// camelCase
		"onCall", "resourcePolicy", "externalChecks",
		// one word
		"summary", "runbook", "owners", "links", "unit", "collection",
		"namespace", "docs", "dashboard", "repository", "other", "queued",
		"running", "succeeded", "failed", "canceled", "allow", "deny",
		// kebab-case
		"awaiting-approval", "every-6-hours", "in-sync", "unit-busy",
		"phase-done", "last-phase-done", "state-observed", "plan-display",
		"change-order", "self-service-revision", "state-write",
		"review-request-push",
		// dot and colon
		"mfa.fresh-step-required", "server:observe",
	}

	enumValue := enumValuePattern()

	for _, v := range wireValues {
		if !enumValue.MatchString(v) {
			t.Errorf("real wire value %q does not match the enum allowlist", v)
		}
	}

	refused := []string{
		"", "OnCall", "1st", "-a", "a-", "a--b", "a..b", "a b", "a/b", "a\nb", "a__", "_a",
		"a:", "é",
	}

	for _, v := range refused {
		if enumValue.MatchString(v) {
			t.Errorf("value %q matches the enum allowlist, want refusal", v)
		}
	}
}
