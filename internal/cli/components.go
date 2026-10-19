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
//nolint:gochecknoglobals // The table is a fixed declaration list; callers never mutate it.
var componentOnly = map[string]string{
	"console-dir":                     ComponentAPI,
	"allowed-origins":                 ComponentAPI,
	"public-url":                      ComponentAPI,
	"trusted-proxies":                 ComponentAPI,
	"trusted-proxy-header":            ComponentAPI,
	"ip-allowlists":                   ComponentAPI,
	"ip-allowlists-include-bootstrap": ComponentAPI,
	"oidc-issuer":                     ComponentAPI,
	"oidc-provider":                   ComponentAPI,
	"oidc-display-name":               ComponentAPI,
	"oidc-client-id":                  ComponentAPI,
	"oidc-scopes":                     ComponentAPI,
	"oidc-no-auto-create":             ComponentAPI,
	"blob-cache-bytes":                ComponentAPI,
	"max-checkpoint-bytes":            ComponentAPI,
	"checkpoint-delta-cutoff":         ComponentAPI,
	"disable-pulumi-journaling":       ComponentAPI,
	"max-secret-batch-bytes":          ComponentAPI,
	"max-state-bytes":                 ComponentAPI,
	"max-body-bytes-in-flight":        ComponentAPI,
	"soft-delete-retention":           ComponentWorker,
	"audit-sink":                      ComponentWorker,
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
