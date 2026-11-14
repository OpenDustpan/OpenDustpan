package access

import (
	"context"
	"errors"
	"fmt"
	"strconv"

	"opendustpan.org/opendustpan/pkg/setting"
)

// SettingCollectionUnitCap is the key of the installation setting that caps
// the count of units in the export of a collection. The settings resource
// shows the effective value and its source.
const SettingCollectionUnitCap = "accessReview.collectionUnitCap"

// DefaultCollectionUnitCap is the cap of the export of a collection when no
// administrator set another one.
const DefaultCollectionUnitCap = 10000

// errCollectionUnitCap is the reason of a value that the setting refuses. The
// settings resource shows the text, so it holds no value.
var errCollectionUnitCap = errors.New("the value is not a whole number of at least 1")

// ParseCollectionUnitCap parses the value of SettingCollectionUnitCap. It
// returns an error for a value that is not a whole number of at least 1.
func ParseCollectionUnitCap(value string) (int, error) {
	n, err := strconv.Atoi(value)
	if err != nil || n < 1 {
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
			ReducesSecurity: nil,
			Doc: fmt.Sprintf("The largest count of units that the export of a collection reads. The default is %d.",
				DefaultCollectionUnitCap),
		},
	}
}

// collectionUnitCap returns the cap of the export of a collection: the
// effective value of SettingCollectionUnitCap. It fails toward the default:
// no reader, a reader that fails, or a value that is not a whole number of
// at least 1 gives DefaultCollectionUnitCap, never no limit.
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
