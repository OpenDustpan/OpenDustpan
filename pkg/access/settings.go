package access

import (
	"context"
	"fmt"
	"strconv"

	"opendustpan.org/opendustpan/pkg/setting"
)

// SettingCollectionUnitCap is the key of the installation setting that caps
// the count of units in the export of a collection. The settings resource
// shows the effective value and its source.
const SettingCollectionUnitCap = "accessReview.collectionUnitCap"

// SettingInstallationGrantsReachNamespaces is the key of the installation
// setting that decides whether a grant on the installation reaches the
// actions inside each namespace. The settings resource shows the effective
// value and its source.
const SettingInstallationGrantsReachNamespaces = "access.installationGrantsReachNamespaces"

// DefaultInstallationGrantsReachNamespaces is the value of
// SettingInstallationGrantsReachNamespaces when no administrator set another
// one. A grant on the installation then reaches each namespace.
const DefaultInstallationGrantsReachNamespaces = "true"

// Bounds of the cap of the export of a collection.
const (
	// DefaultCollectionUnitCap is the cap of the export of a collection when
	// no administrator set another one.
	DefaultCollectionUnitCap = 10000
	// MinCollectionUnitCap is the smallest cap that the setting accepts.
	MinCollectionUnitCap = 1
	// MaxCollectionUnitCap is the largest cap that the setting accepts.
	MaxCollectionUnitCap = 100000
)

// errCollectionUnitCap is the reason of a value that the setting refuses. The
// settings resource shows the text, so it holds no value.
var errCollectionUnitCap = fmt.Errorf("the value is not a whole number from %d to %d",
	MinCollectionUnitCap, MaxCollectionUnitCap)

// ParseCollectionUnitCap parses the value of SettingCollectionUnitCap. It
// returns an error for a value that is not a whole number of
// MinCollectionUnitCap to MaxCollectionUnitCap.
func ParseCollectionUnitCap(value string) (int, error) {
	n, err := strconv.Atoi(value)
	if err != nil || n < MinCollectionUnitCap || n > MaxCollectionUnitCap {
		return 0, errCollectionUnitCap
	}

	return n, nil
}

// SettingDeclarations returns the declaration of the settings of the access
// review, for the settings resource of the installation. The database layer
// holds the cap, and the static layer can lock it.
func SettingDeclarations() []setting.Declaration {
	return append(accessReviewDeclarations(), installationReachDeclaration())
}

// accessReviewDeclarations returns the declarations of the access review: the
// cap of the export of a collection.
func accessReviewDeclarations() []setting.Declaration {
	return []setting.Declaration{
		{
			Key: SettingCollectionUnitCap, Level: setting.LevelInstallation, Kind: setting.KindInt,
			Default: strconv.Itoa(DefaultCollectionUnitCap), Secret: false, Database: true,
			StaticCredentialHost: false,
			Apply:                setting.ApplyLive,
			Check: func(value string) error {
				_, err := ParseCollectionUnitCap(value)

				return err
			},
			// A larger cap lets the export of a collection read more units.
			ReducesSecurity: func(old, next string) bool {
				o, oErr := ParseCollectionUnitCap(old)
				n, nErr := ParseCollectionUnitCap(next)

				return oErr != nil || nErr != nil || n > o
			},
			Doc: fmt.Sprintf(
				"The largest count of units that the export of a collection reads, from %d to %d. The default is %d.",
				MinCollectionUnitCap, MaxCollectionUnitCap, DefaultCollectionUnitCap,
			),
		},
	}
}

// installationReachDeclaration returns the declaration of
// SettingInstallationGrantsReachNamespaces. The database layer holds the value,
// and the static layer can lock it. A change to true lets a grant on the
// installation reach each namespace again, so it reduces security.
func installationReachDeclaration() setting.Declaration {
	return setting.Declaration{
		Key: SettingInstallationGrantsReachNamespaces, Level: setting.LevelInstallation, Kind: setting.KindBool,
		Default: DefaultInstallationGrantsReachNamespaces, Secret: false, Database: true,
		StaticCredentialHost: false,
		Apply:                setting.ApplyLive,
		Check:                nil,
		ReducesSecurity: func(old, next string) bool {
			return old == "false" && next == "true"
		},
		Doc: "Whether a grant on the installation reaches the actions inside each namespace. " +
			"The default is true. With false, a grant on the installation gives only the operator " +
			"actions, and no action inside a namespace. A grant on a namespace is not affected.",
	}
}

// InstallationGrantsReachNamespaces returns the effective value of
// SettingInstallationGrantsReachNamespaces: true when a grant on the
// installation reaches the actions inside each namespace. A nil settings port
// answers the default, true. A failed read, and a value that is not "true" or
// "false", return an error, and the caller must deny the decision.
func InstallationGrantsReachNamespaces(ctx context.Context, settings Settings) (bool, error) {
	if settings == nil {
		return true, nil
	}

	item, err := settings.Effective(ctx, setting.InstallationScope(), SettingInstallationGrantsReachNamespaces)
	if err != nil {
		return false, fmt.Errorf("read setting %s: %w", SettingInstallationGrantsReachNamespaces, err)
	}

	switch item.Value {
	case "true":
		return true, nil
	case "false":
		return false, nil
	default:
		return false, fmt.Errorf("%w: setting %s is neither true nor false", ErrInvalid,
			SettingInstallationGrantsReachNamespaces)
	}
}

// FixedSettings returns a settings port that answers the value of
// SettingInstallationGrantsReachNamespaces for reaches, for a test or for a
// fixed deployment. Any other key is refused with the not-found error of the
// settings port.
func FixedSettings(reaches bool) FixedSettingsPort {
	return FixedSettingsPort{reaches: reaches}
}

// FixedSettingsPort is the settings port that FixedSettings returns.
type FixedSettingsPort struct {
	reaches bool
}

// Effective answers the value of the reach setting, and refuses any other key.
func (f FixedSettingsPort) Effective(_ context.Context, _ setting.Scope, key string) (setting.Item, error) {
	if key != SettingInstallationGrantsReachNamespaces {
		return setting.Item{}, fmt.Errorf("%w: setting %s", ErrNotFound, key)
	}

	value := strconv.FormatBool(f.reaches)

	return setting.Effective(installationReachDeclaration(), &value, nil), nil
}

// collectionUnitCap returns the cap of the export of a collection: the
// effective value of SettingCollectionUnitCap. It fails toward the default:
// no reader, a reader that fails, or a value outside the bounds of
// MinCollectionUnitCap to MaxCollectionUnitCap gives DefaultCollectionUnitCap,
// never no limit.
func collectionUnitCap(ctx context.Context, settings Settings) int {
	if settings == nil {
		return DefaultCollectionUnitCap
	}

	item, err := settings.Effective(ctx, setting.InstallationScope(), SettingCollectionUnitCap)
	if err != nil {
		return DefaultCollectionUnitCap
	}

	n, err := ParseCollectionUnitCap(item.Value)
	if err != nil {
		return DefaultCollectionUnitCap
	}

	return n
}

// boundCollectionUnitCap keeps n in the bounds of SettingCollectionUnitCap. A
// value below MinCollectionUnitCap gives DefaultCollectionUnitCap, and a value
// above MaxCollectionUnitCap gives MaxCollectionUnitCap.
func boundCollectionUnitCap(n int) int {
	if n < MinCollectionUnitCap {
		return DefaultCollectionUnitCap
	}

	if n > MaxCollectionUnitCap {
		return MaxCollectionUnitCap
	}

	return n
}
