package dustpan

import (
	"fmt"

	"opendustpan.org/opendustpan/pkg/setting"
)

// settingsConfig holds what WithSettings sets.
type settingsConfig struct {
	store        setting.Store
	declarations []setting.Declaration
	static       setting.Static
}

// WithSettings sets the store, the declarations and the static layer of the
// settings resource. With a store, the server serves
// GET and PATCH on /api/dustpan/v1/settings and on
// /api/dustpan/v1/namespaces/{namespace}/settings. Without one, the routes
// answer 501.
//
// The static layer holds the values that a flag, an environment variable or
// the configuration file give. It wins over the database layer and locks the
// setting. Nil means no static value. A declaration that is not valid, or a key
// that two declarations share, makes NewServer fail.
func WithSettings(store setting.Store, declarations []setting.Declaration, static setting.Static) Option {
	return func(o *settings) {
		o.settingsConfig = settingsConfig{store: store, declarations: declarations, static: static}
	}
}

// checkSettingDeclarations refuses declarations that are not valid.
func checkSettingDeclarations(cfg *settings) error {
	if cfg.settingsConfig.store == nil {
		return nil
	}

	if _, err := setting.NewRegistry(cfg.settingsConfig.declarations); err != nil {
		return fmt.Errorf("settings: %w", err)
	}

	return nil
}

// newSettingService builds the settings service from what WithSettings set,
// or returns nil when it set no store. A nil service makes the routes answer
// 501. checkSettingDeclarations has checked the declarations already.
func newSettingService(cfg *settings, namespaces setting.Namespaces) *setting.Service {
	if cfg.settingsConfig.store == nil {
		return nil
	}

	registry, err := setting.NewRegistry(cfg.settingsConfig.declarations)
	if err != nil {
		// Defense in depth: NewServer checks the declarations before it builds
		// the service. A set of declarations that fails here gives no service,
		// so the routes answer 501 and never serve a registry that is not valid.
		return nil
	}

	return setting.NewService(cfg.settingsConfig.store, registry, cfg.settingsConfig.static, namespaces, cfg.now)
}
