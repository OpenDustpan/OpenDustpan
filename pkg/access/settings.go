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
