// Package auditrequired_test proves that no HTTP adapter accepts a missing
// audit store: a refusal or an override with no entry is a failure of the
// audit log, so each adapter refuses to start without the store.
package auditrequired_test

import (
	"errors"
	"strings"
	"testing"

	apiaudit "opendustpan.org/opendustpan/internal/api/audit"
	apiinventory "opendustpan.org/opendustpan/internal/api/inventory"
	apijobs "opendustpan.org/opendustpan/internal/api/jobs"
	apinotifications "opendustpan.org/opendustpan/internal/api/notifications"
	apicontext "opendustpan.org/opendustpan/internal/api/objectcontext"
	apiprofile "opendustpan.org/opendustpan/internal/api/profile"
	apipulumi "opendustpan.org/opendustpan/internal/api/pulumi"
	apiregistry "opendustpan.org/opendustpan/internal/api/registry"
	apirunner "opendustpan.org/opendustpan/internal/api/runnerapi"
	apiserver "opendustpan.org/opendustpan/internal/api/server"
	apitfe "opendustpan.org/opendustpan/internal/api/tfe"
	apitofuhttp "opendustpan.org/opendustpan/internal/api/tofuhttp"
)

// TestEveryAdapterNeedsAnAuditStore builds each adapter with no audit store.
// Each NewHandler must answer its ErrMissingDependency and name the audit
// store. The Config is empty on purpose: the audit store is the first thing
// that each adapter checks, so nothing else can hide the missing store.
func TestEveryAdapterNeedsAnAuditStore(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		build func() error
		want  error
	}{
		{"audit", func() error { _, err := apiaudit.NewHandler(apiaudit.Config{}); return err }, apiaudit.ErrMissingDependency},
		{"inventory", func() error { _, err := apiinventory.NewHandler(apiinventory.Config{}); return err }, apiinventory.ErrMissingDependency},
		{"jobs", func() error { _, err := apijobs.NewHandler(apijobs.Config{}); return err }, apijobs.ErrMissingDependency},
		{"notifications", func() error { _, err := apinotifications.NewHandler(apinotifications.Config{}); return err }, apinotifications.ErrMissingDependency},
		{"context", func() error { _, err := apicontext.NewHandler(apicontext.Config{}); return err }, apicontext.ErrMissingDependency},
		{"profile", func() error { _, err := apiprofile.NewHandler(apiprofile.Config{}); return err }, apiprofile.ErrMissingDependency},
		{"pulumi", func() error { _, err := apipulumi.NewHandler(apipulumi.Config{}); return err }, apipulumi.ErrMissingDependency},
		{"registry", func() error { _, err := apiregistry.NewHandler(apiregistry.Config{}); return err }, apiregistry.ErrMissingDependency},
		{"runner", func() error { _, err := apirunner.NewHandler(apirunner.Config{}); return err }, apirunner.ErrMissingDependency},
		{"server", func() error { _, err := apiserver.NewHandler(apiserver.Config{}); return err }, apiserver.ErrMissingDependency},
		{"tfe", func() error { _, err := apitfe.NewHandler(apitfe.Config{}); return err }, apitfe.ErrMissingDependency},
		{"tofu http", func() error { _, err := apitofuhttp.NewHandler(apitofuhttp.Config{}); return err }, apitofuhttp.ErrMissingDependency},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			err := tt.build()
			if !errors.Is(err, tt.want) {
				t.Fatalf("NewHandler with no audit store = %v, want %v", err, tt.want)
			}

			if !strings.Contains(err.Error(), "audit store") {
				t.Fatalf("error = %q, want it to name the audit store", err)
			}
		})
	}
}
