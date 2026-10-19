package cli

import "slices"

// Component names a part of the server that a process runs. The names are
// the values of --components.
const (
	ComponentAPI    = "api"
	ComponentWorker = "worker"
)

// componentOnly lists the serve flags that only one component reads. A flag
// that is not in the table is read by every component. A process that sets
// such a flag and runs no component that reads it ignores the flag and logs
// that at start.
//
// A flag that the worker component reads too is not in the table, even when
// the api component reads it first: --soft-delete-retention sets the end of
// the retention that the api component records and that the worker deletes
// at, --audit-sink is listed by the api component and shipped to by the
// worker, and --blob-cache-bytes wraps the blob store that the worker reads.
// TestComponentOnlyMatchesReaders in cmd/opendustpan checks each entry
// against the code that reads the setting.
//
// A flag that a process with only the worker component reads too is not in the
// table: --allowed-origins sets the CORS rule of the metrics route, and
// --trusted-proxies and --trusted-proxy-header set the client address in the
// request log of every route. The IP allowlists check the namespace of an
// object. The metrics route has no namespace, so the allowlists gate no
// route of a worker-only process.
//
//nolint:gochecknoglobals // The table is a fixed declaration list; callers never mutate it.
var componentOnly = map[string]string{
	"console-dir":                     ComponentAPI,
	"public-url":                      ComponentAPI,
	"ip-allowlists":                   ComponentAPI,
	"ip-allowlists-include-bootstrap": ComponentAPI,
	"oidc-issuer":                     ComponentAPI,
	"oidc-provider":                   ComponentAPI,
	"oidc-display-name":               ComponentAPI,
	"oidc-client-id":                  ComponentAPI,
	"oidc-scopes":                     ComponentAPI,
	"oidc-no-auto-create":             ComponentAPI,
	"max-checkpoint-bytes":            ComponentAPI,
	"checkpoint-delta-cutoff":         ComponentAPI,
	"disable-pulumi-journaling":       ComponentAPI,
	"max-secret-batch-bytes":          ComponentAPI,
	"max-state-bytes":                 ComponentAPI,
	"max-body-bytes-in-flight":        ComponentAPI,
}

// UnreadFlags answers the names of the settings that a layer other than the
// default set and that no component in selected reads, in the order of
// resolved. The server ignores them.
func UnreadFlags(resolved []Resolved, selected []string) []string {
	var out []string

	for _, r := range resolved {
		if r.Source == SourceDefault || r.Setting.Flag == "" {
			continue
		}

		only, limited := componentOnly[r.Setting.Flag]
		if limited && !slices.Contains(selected, only) {
			out = append(out, r.Setting.Name())
		}
	}

	return out
}

// ComponentOnly answers the one component that reads the flag, or the empty
// string when more than one component reads it.
func ComponentOnly(flag string) string {
	return componentOnly[flag]
}

// ComponentOnlyFlags lists the flags that the table limits to one component,
// in name order.
func ComponentOnlyFlags() []string {
	flags := make([]string, 0, len(componentOnly))
	for flag := range componentOnly {
		flags = append(flags, flag)
	}

	slices.Sort(flags)

	return flags
}
