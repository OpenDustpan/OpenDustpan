// Package auditrequired_test proves that no HTTP adapter accepts a missing
// audit store: a refusal or an override with no entry is a failure of the
// audit log, so each adapter refuses to start without the store.
package auditrequired_test

import (
	"errors"
	"strings"
	"testing"

	apiaudit "opendustpan.org/opendustpan/internal/api/audit"
	apidrift "opendustpan.org/opendustpan/internal/api/drift"
	apiinventory "opendustpan.org/opendustpan/internal/api/inventory"
	apijobs "opendustpan.org/opendustpan/internal/api/jobs"
	apinotifications "opendustpan.org/opendustpan/internal/api/notifications"
	apicontext "opendustpan.org/opendustpan/internal/api/objectcontext"
	apiprofile "opendustpan.org/opendustpan/internal/api/profile"
	apipulumi "opendustpan.org/opendustpan/internal/api/pulumi"
	apiregistry "opendustpan.org/opendustpan/internal/api/registry"
	apirunner "opendustpan.org/opendustpan/internal/api/runnerapi"
	apiserver "opendustpan.org/opendustpan/internal/api/server"
	apisettings "opendustpan.org/opendustpan/internal/api/settings"
	apitfe "opendustpan.org/opendustpan/internal/api/tfe"
	apitofuhttp "opendustpan.org/opendustpan/internal/api/tofuhttp"
	apitriggers "opendustpan.org/opendustpan/internal/api/triggers"
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
		{
			"audit",
			zeroConfig(apiaudit.NewHandler),
			apiaudit.ErrMissingDependency,
		},
		{
			"inventory",
			zeroConfig(apiinventory.NewHandler),
			apiinventory.ErrMissingDependency,
		},
		{
			"jobs",
			zeroConfig(apijobs.NewHandler),
			apijobs.ErrMissingDependency,
		},
		{
			"notifications",
			zeroConfig(apinotifications.NewHandler),
			apinotifications.ErrMissingDependency,
		},
		{
			"context",
			zeroConfig(apicontext.NewHandler),
			apicontext.ErrMissingDependency,
		},
		{
			"drift",
			zeroConfig(apidrift.NewHandler),
			apidrift.ErrMissingDependency,
		},
		{
			"profile",
			zeroConfig(apiprofile.NewHandler),
			apiprofile.ErrMissingDependency,
		},
		{
			"pulumi",
			zeroConfig(apipulumi.NewHandler),
			apipulumi.ErrMissingDependency,
		},
		{
			"registry",
			zeroConfig(apiregistry.NewHandler),
			apiregistry.ErrMissingDependency,
		},
		{
			"runner",
			zeroConfig(apirunner.NewHandler),
			apirunner.ErrMissingDependency,
		},
		{
			"server",
			zeroConfig(apiserver.NewHandler),
			apiserver.ErrMissingDependency,
		},
		{"settings", zeroConfig(apisettings.NewHandler), apisettings.ErrMissingDependency},
		{"tfe", zeroConfig(apitfe.NewHandler), apitfe.ErrMissingDependency},
		{
			"triggers",
			zeroConfig(apitriggers.NewHandler),
			apitriggers.ErrMissingDependency,
		},
		{
			"tofu http",
			zeroConfig(apitofuhttp.NewHandler),
			apitofuhttp.ErrMissingDependency,
		},
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

// zeroConfig answers the function that calls newHandler with the zero Config.
func zeroConfig[C, H any](newHandler func(C) (H, error)) func() error {
	return func() error {
		var cfg C

		_, err := newHandler(cfg)

		return err
	}
}
