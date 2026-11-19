package dustpan

import (
	"context"
	"fmt"
	"time"

	"opendustpan.org/opendustpan/pkg/backup"
	"opendustpan.org/opendustpan/pkg/gate"
	"opendustpan.org/opendustpan/pkg/ipallow"
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

// errRetentionRange is the reason of a refused retention value.
var errRetentionRange = fmt.Errorf("give 0 (keep forever) or a whole number of hours from %d to %d",
	gate.MinRetentionHours, gate.MaxRetentionHours)

// retentionCheck runs the check of the gate package and gives the settings
// resource a reason that holds no package prefix and does not repeat the key:
// the answer names the key itself.
func retentionCheck(value string) error {
	if gate.RetentionCheck(value) != nil {
		return errRetentionRange
	}

	return nil
}

// decisionRecordRetentionDeclaration returns the declaration of the retention
// period of the decision records, in whole hours. The default "0" keeps the
// records forever. The setting prunes decision records only and never an audit
// entry. No job reads it yet, so the setting alone deletes nothing. A change
// to a shorter period, or from keep-forever to a period, needs a fresh
// authentication, because it deletes evidence sooner.
func decisionRecordRetentionDeclaration() setting.Declaration {
	return setting.Declaration{
		Key: gate.RetentionKey, Level: setting.LevelInstallation, Kind: setting.KindInt, Default: "0",
		Secret: false, Database: true, StaticCredentialHost: false, Apply: setting.ApplyLive,
		// The kind check of the setting package and RetentionCheck both run, so
		// the declaration does not rely on one of them alone.
		Check:           retentionCheck,
		ReducesSecurity: gate.RetentionReducesSecurity,
		Doc: fmt.Sprintf("The hours that the server keeps decision records, from %d to %d. 0 keeps decision "+
			"records forever. The setting never deletes an audit entry. No job deletes decision records yet.",
			gate.MinRetentionHours, gate.MaxRetentionHours),
	}
}

// settingDeclarations returns the declarations of the settings resource: the
// declarations of WithSettings and the settings that the server declares
// itself.
func settingDeclarations(cfg *settings) []setting.Declaration {
	allowlist := ipallow.SettingDeclarations()

	out := make([]setting.Declaration, 0, len(cfg.settingsConfig.declarations)+len(allowlist))
	out = append(out, cfg.settingsConfig.declarations...)
	out = append(out, allowlist...)

	out = append(out, decisionRecordRetentionDeclaration(), drainPeriodDeclaration(cfg.drainPeriod))

	return out
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

// restoreAccessSettingStaticReader returns the reader that says whether the
// static layer sets the restore access setting, or nil when the server keeps
// no settings.
func restoreAccessSettingStaticReader(cfg *settings) func(context.Context) (bool, error) {
	svc := newSettingService(cfg, nil)
	if svc == nil {
		return nil
	}

	return func(ctx context.Context) (bool, error) {
		item, err := svc.Effective(ctx, setting.InstallationScope(), backup.SettingRestoreAccessTables)
		if err != nil {
			return false, fmt.Errorf("read the restore access setting: %w", err)
		}

		return item.Source != setting.SourceDatabase && item.Source != setting.SourceDefault, nil
	}
}

// restoreAccessSettingReader returns the reader of the stored text of the
// restore access setting, or nil when the server keeps no settings. A key that
// the declarations do not hold makes the reader fail, and the restore then
// keeps the access tables.
func restoreAccessSettingReader(cfg *settings) func(context.Context) (string, error) {
	svc := newSettingService(cfg, nil)
	if svc == nil {
		return nil
	}

	return func(ctx context.Context) (string, error) {
		item, err := svc.Effective(ctx, setting.InstallationScope(), backup.SettingRestoreAccessTables)
		if err != nil {
			return "", fmt.Errorf("read the restore access setting: %w", err)
		}

		return item.Value, nil
	}
}
