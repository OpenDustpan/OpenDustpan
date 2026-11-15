package main

import (
	"context"
	"fmt"
	"log/slog"

	"opendustpan.org/opendustpan/internal/cli"
	"opendustpan.org/opendustpan/pkg/dustpan"
	"opendustpan.org/opendustpan/pkg/setting"
)

// settingsWiring holds what serve needs for the settings resource: the option
// that mounts it, and the function that records the static layer of this
// process in the store. A store that keeps no settings gives the zero value.
type settingsWiring struct {
	options []dustpan.Option
	sync    func(ctx context.Context) error
}

// staticLayer returns the static layer of the settings resource: the resolved
// serve settings, which a reload of the configuration file replaces.
func (f *serveFlags) staticLayer() *cli.StaticLayer {
	if f.layer == nil {
		f.layer = cli.NewStaticLayer(f.resolved)
	}

	return f.layer
}

// settingsWiring returns the settings wiring for st.
func (f *serveFlags) settingsWiring(st store) (settingsWiring, error) {
	builtin := st.builtin()

	return f.wireSettings(builtin.SettingStore(), storeSettingDeclarations(builtin)...)
}

// storeSettingDeclarations returns the declarations of the settings of the
// store itself: the bounds of a PostgreSQL store, and none for a SQLite
// store.
func storeSettingDeclarations(b *dustpan.BuiltinStore) []setting.Declaration {
	return b.SettingDeclarations()
}

// wireSettings builds the wiring over the settings store ss. extra holds the
// declarations of the settings of the store itself, when the store has any.
func (f *serveFlags) wireSettings(ss setting.Store, extra ...setting.Declaration) (settingsWiring, error) {
	declarations := append(cli.SettingDeclarations(), extra...)

	registry, err := setting.NewRegistry(declarations)
	if err != nil {
		return settingsWiring{options: nil, sync: nil}, fmt.Errorf("settings resource: %w", err)
	}

	layer := f.staticLayer()

	return settingsWiring{
		options: []dustpan.Option{dustpan.WithSettings(ss, declarations, layer)},
		sync: func(ctx context.Context) error {
			if _, _, syncErr := ss.Sync(ctx, registry.Digest(layer)); syncErr != nil {
				return fmt.Errorf("record the static layer of the settings: %w", syncErr)
			}

			return nil
		},
	}, nil
}

// syncSettings records the static layer of this process in the store, so the
// revision of the settings resource changes when the layer changed an
// effective value or a source. A failure never stops the process: the lock
// rules read the static layer at each change, so only the revision lags. It
// writes one WARN line.
func (f *serveFlags) syncSettings(ctx context.Context, logger *slog.Logger) {
	if f.settingsSync == nil {
		return
	}

	if err := f.settingsSync(ctx); err != nil {
		logger.WarnContext(ctx, "settings revision not updated", slog.String("error", err.Error()))
	}
}
