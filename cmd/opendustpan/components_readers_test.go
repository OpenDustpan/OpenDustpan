package main

import (
	"go/ast"
	"go/parser"
	"go/token"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/internal/cli"
)

// settingField names the field of the server settings that a serve flag sets.
// A flag that is not here is read by every component, or is not a server
// setting.
var settingFields = map[string]string{ //nolint:gochecknoglobals // A fixed declaration table.
	"console-dir":                     "consoleDir",
	"allowed-origins":                 "allowedOrigins",
	"console-content-type-options":    "consoleHeaders",
	"console-frame-options":           "consoleHeaders",
	"console-csp":                     "consoleHeaders",
	"console-hsts":                    "consoleHeaders",
	"public-url":                      "publicURL",
	"issuer-base-url":                 "issuerBaseURL",
	"trusted-proxies":                 "proxies",
	"trusted-proxy-header":            "proxies",
	"ip-allowlists":                   "ipAllowlists",
	"ip-allowlists-include-bootstrap": "ipAllowlists",
	"git-host-allowed-ranges":         "gitHost",
	"oidc-issuer":                     "oidcProviders",
	"oidc-provider":                   "oidcProviders",
	"oidc-display-name":               "oidcProviders",
	"oidc-client-id":                  "oidcProviders",
	"oidc-scopes":                     "oidcProviders",
	"oidc-no-auto-create":             "oidcProviders",
	"blob-cache-bytes":                "blobCacheBytes",
	"max-checkpoint-bytes":            "maxCheckpointBytes",
	"checkpoint-delta-cutoff":         "checkpointDeltaCutoff",
	"disable-pulumi-journaling":       "disablePulumiJournaling",
	"max-secret-batch-bytes":          "maxSecretBatchBytes",
	"max-state-bytes":                 "maxStateBytes",
	"max-body-bytes-in-flight":        "maxBodyBytesInFlight",
	"soft-delete-retention":           "softDeleteRetention",
	"audit-sink":                      "auditSinks",
	"destination-ports":               "sinks",
}

// readerComponent names, for each function that reads a settings field, the
// components that the read affects. "api" means the read changes only what the
// HTTP routes do. "all" means the read also changes what a process does that
// runs only the worker component. A new reader fails the test until someone
// decides which of the two it is.
//
// The worker component runs the job sweeps (the inactivity disable, the
// deleted-object purge, the pruning of sign-in requests and of replay
// records), the audit shipper and the audit chain check. A function that
// builds a service for the routes is "api" even though every process builds it.
// The metrics route of a worker-only process takes the CORS rule, and the
// request log of that process takes the client address rule, so buildMux and
// routes are "all". The IP allowlists check the namespace of an
// object, and the metrics route has none, so newAuthPorts stays "api".
var readerComponent = map[string]map[string]string{ //nolint:gochecknoglobals // A fixed declaration table.
	"consoleDir":     {"mountAPI": "api"},
	"consoleHeaders": {"checkSettings": "api", "mountAPI": "api"},
	"allowedOrigins": {"buildMux": "all", "checkSettings": "api", "mountAPI": "api", "newSignIn": "api"},
	"publicURL": {
		"checkSettings":       "api",
		"newExchanger":        "api",
		"newInventoryHandler": "api",
		"newIssuerService":    "api",
		"newRunnerHandler":    "api",
		"newServerExtras":     "api",
		"newSignIn":           "api",
		"newTrustRuleService": "api",
		"storedSignInReady":   "api",
	},
	"issuerBaseURL": {"checkSettings": "api", "newIssuerService": "api"},
	"proxies":       {"routes": "all"},
	"ipAllowlists": {
		"checkTLSAndAllowlist": "api", "newServerExtras": "api", "newPulumiHandler": "api",
		"newAuthPorts": "api", "newTFEHandler": "api",
	},
	// The ranges apply to the connections that the routes of a namespace make.
	"gitHost": {"newGitHostService": "api", "newJobPlanner": "api", "newUnitSettingsHandler": "api"},
	"oidcProviders": {
		"bindingOf":             "api",
		"hasStaticSignIn":       "api",
		"identityProviders":     "api",
		"newProviderAdmin":      "api",
		"staticSignInProviders": "api",
	},
	// The cache wraps the blob store that the purge of deleted objects also uses.
	"blobCacheBytes":          {"wrapBlobCache": "all"},
	"maxCheckpointBytes":      {"journalWriter": "api", "newUpdateService": "api"},
	"checkpointDeltaCutoff":   {"newPulumiHandler": "api"},
	"disablePulumiJournaling": {"newPulumiHandler": "api"},
	"maxSecretBatchBytes":     {"newServices": "api"},
	"maxStateBytes":           {"newTFEHandler": "api", "newWorkspaceService": "api"},
	"maxBodyBytesInFlight":    {"newAdapters": "api", "newPulumiHandler": "api"},
	// The api component records the end of the retention of a delete in
	// PurgeAfter. The worker component purges at that end.
	"softDeleteRetention": {"checkSettings": "all", "newServices": "all", "newWorkspaceService": "all"},
	// The api component lists the sinks. The worker component ships to them.
	"auditSinks": {"newAuditShipper": "all"},
	// The sink routes and the built-in webhook sender take the ports. Both
	// serve the api component only. allHandlers names the adapter field of the
	// same name for the description of the routes. The removal alert service
	// writes to the outbox of the sinks for the sign-in routes only.
	"sinks": {
		"allHandlers": "api", "mountAPI": "api", "newSinksHandler": "api",
		"removalAlertServiceOf": "api", "sinkSenders": "api",
	},
}

// readersOf returns, for each field, the names of the functions of package
// dustpan that read it. The setter functions (With...) and the allowlist
// accessor write the field, so they are not readers.
func readersOf(t *testing.T, fields map[string]bool) map[string][]string {
	t.Helper()

	files, err := filepath.Glob(filepath.Join("..", "..", "pkg", "dustpan", "*.go"))
	if err != nil {
		t.Fatalf("glob: %v", err)
	}

	found := map[string]map[string]bool{}
	fset := token.NewFileSet()

	for _, file := range files {
		if strings.HasSuffix(file, "_test.go") {
			continue
		}

		src, readErr := os.ReadFile(file) //nolint:gosec // The path comes from a glob of the repository.
		if readErr != nil {
			t.Fatalf("read %s: %v", file, readErr)
		}

		parsed, parseErr := parser.ParseFile(fset, file, src, 0)
		if parseErr != nil {
			t.Fatalf("parse %s: %v", file, parseErr)
		}

		collectReaders(parsed, fields, found)
	}

	out := map[string][]string{}
	for field, names := range found {
		out[field] = slices.Sorted(maps.Keys(names))
	}

	return out
}

// collectReaders adds the functions of file that select a field of fields.
func collectReaders(file *ast.File, fields map[string]bool, found map[string]map[string]bool) {
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || strings.HasPrefix(fn.Name.Name, "With") || fn.Name.Name == "ipAllowlistConfig" {
			continue
		}

		ast.Inspect(fn, func(n ast.Node) bool {
			sel, isSel := n.(*ast.SelectorExpr)
			if isSel && fields[sel.Sel.Name] {
				if found[sel.Sel.Name] == nil {
					found[sel.Sel.Name] = map[string]bool{}
				}

				found[sel.Sel.Name][fn.Name.Name] = true
			}

			return true
		})
	}
}

// TestComponentOnlyMatchesReaders ties the table of settings that only one
// component reads to the code that reads each setting. A change that adds a
// reader, or moves a read to the worker component, fails here until the table
// and the classification above agree with the code.
func TestComponentOnlyMatchesReaders(t *testing.T) {
	t.Parallel()

	fields := map[string]bool{}
	for _, field := range settingFields {
		fields[field] = true
	}

	readers := readersOf(t, fields)

	for field := range fields {
		classified := readerComponent[field]

		got := readers[field]
		if want := slices.Sorted(maps.Keys(classified)); !slices.Equal(got, want) {
			t.Errorf("readers of %s = %v, classified %v: classify the new reader in readerComponent", field, got, want)
		}
	}

	for flag, field := range settingFields {
		apiOnly := true

		for _, component := range readerComponent[field] {
			if component != "api" {
				apiOnly = false
			}
		}

		if got := cli.ComponentOnly(flag); apiOnly != (got == cli.ComponentAPI) {
			t.Errorf("flag %s: readers of %s are api-only = %v, table says %q", flag, field, apiOnly, got)
		}
	}

	for _, flag := range cli.ComponentOnlyFlags() {
		if _, ok := settingFields[flag]; !ok {
			t.Errorf("flag %s is in the table but has no setting field in settingFields", flag)
		}
	}
}
