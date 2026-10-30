package dustpan

import (
	"fmt"
	"net/http"

	settingsapi "opendustpan.org/opendustpan/internal/api/settings"
	"opendustpan.org/opendustpan/internal/unitsettingssvc"
	"opendustpan.org/opendustpan/pkg/unitsettings"
)

// WithUnitSettingsStore sets the store of the settings of units. With it, the
// Git host store (WithGitHostStore) and the runner stores (WithRunners), the
// server serves the unit settings API: the settings of a unit under
// /api/dustpan/v1/namespaces/{namespace}/units/{unit_id}/settings, the unit
// defaults of a namespace and the bulk call. Without one of them, the server
// serves none.
func WithUnitSettingsStore(st unitsettings.Store) Option {
	return func(o *settings) { o.unitSettingsStore = st }
}

// triggerAndSettingsHandlers holds the trigger API and the unit settings API.
// Each is nil when the server lacks what it needs.
type triggerAndSettingsHandlers struct {
	triggers     http.Handler
	unitSettings http.Handler
}

// newTriggersAndSettingsHandlers builds the trigger API and the unit settings
// API.
func newTriggersAndSettingsHandlers(svc services, cfg *settings, onFailure func()) (triggerAndSettingsHandlers, error) {
	var out triggerAndSettingsHandlers

	triggers, err := newTriggersHandler(svc, cfg, onFailure)
	if err != nil {
		return out, err
	}

	out.triggers = triggers

	handler, ok, err := newUnitSettingsHandler(svc, cfg, onFailure)
	if err != nil {
		return out, err
	}

	if ok {
		out.unitSettings = handler
	}

	return out, nil
}

// newUnitSettingsHandler builds the unit settings API. The bool is false when
// the server lacks the store of the settings, the Git host store, the runner
// stores or the inventory, which the checks of a write read. It answers
// ErrMissingOption when the namespace store keeps no audit entries.
func newUnitSettingsHandler(svc services, cfg *settings, onFailure func()) (http.Handler, bool, error) {
	entries, err := auditStoreOf(cfg)
	if err != nil {
		return nil, false, err
	}

	if cfg.unitSettingsStore == nil || cfg.gitHost.store == nil || cfg.runners == nil ||
		cfg.inventoryReader == nil || svc.namespaces == nil || cfg.authorizer == nil {
		return nil, false, nil
	}

	service, err := unitsettingssvc.New(unitsettingssvc.Config{
		Store: cfg.unitSettingsStore, Fences: jobFences{namespaces: svc.namespaces},
		Connections: cfg.gitHost.store, Pools: cfg.runners, Now: cfg.now,
	})
	if err != nil {
		return nil, false, fmt.Errorf("unit settings service: %w", err)
	}

	handler, err := settingsapi.NewHandler(settingsapi.Config{
		Namespaces:     svc.namespaces,
		Units:          cfg.inventoryReader,
		Service:        service,
		Suggestions:    nil,
		Authenticator:  cfg.authenticator,
		Authorizer:     cfg.authorizer,
		Audit:          entries,
		OnAuditFailure: onFailure,
		DenialLimiter:  cfg.denialLimiter,
		Sessions:       cfg.sessionCookies,
		Logger:         cfg.logger,
	})
	if err != nil {
		return nil, false, fmt.Errorf("unit settings api: %w", err)
	}

	return handler, true, nil
}
