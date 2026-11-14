package dustpan

import (
	"fmt"
	"time"

	"opendustpan.org/opendustpan/pkg/setting"
)

// drainPeriodKey is the key of the drain period in the settings resource.
const drainPeriodKey = "server.drainPeriod"

// drainPeriodDeclaration returns the declaration of the drain period: the
// time between the stop signal and the start of the shutdown. The value is
// the drain that WithDrainPeriod set. The setting is read only: no database
// layer holds it, and no flag, variable or file key sets it.
func drainPeriodDeclaration(period time.Duration) setting.Declaration {
	return setting.Declaration{
		Key: drainPeriodKey, Level: setting.LevelInstallation, Kind: setting.KindString, Default: period.String(),
		Secret: false, Database: false, StaticCredentialHost: false, Apply: setting.ApplyRestart, Check: nil,
		ReducesSecurity: nil,
		Doc: "The time between the stop signal and the start of the shutdown. In it, GET /health/ready " +
			"answers 503 and the server serves all other requests, so a load balancer takes the instance " +
			"out of its pool.",
	}
}

// settingDeclarations returns the declarations of the settings resource: the
// declarations of WithSettings and the settings that the server declares
// itself.
func settingDeclarations(cfg *settings) []setting.Declaration {
	out := make([]setting.Declaration, 0, len(cfg.settingsConfig.declarations)+1)
	out = append(out, cfg.settingsConfig.declarations...)

	return append(out, drainPeriodDeclaration(cfg.drainPeriod))
}

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

	if _, err := setting.NewRegistry(settingDeclarations(cfg)); err != nil {
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

	registry, err := setting.NewRegistry(settingDeclarations(cfg))
	if err != nil {
		// Defense in depth: NewServer checks the declarations before it builds
		// the service. A set of declarations that fails here gives no service,
		// so the routes answer 501 and never serve a registry that is not valid.
		return nil
	}

	return setting.NewService(cfg.settingsConfig.store, registry, cfg.settingsConfig.static, namespaces, cfg.now)
}
