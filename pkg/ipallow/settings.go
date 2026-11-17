package ipallow

import (
	"fmt"

	"opendustpan.org/opendustpan/pkg/setting"
)

// Keys of the installation settings of the IP allowlist. The settings
// resource shows the effective value and the source of each one.
const (
	// SettingEnabled turns the installation list on or off.
	SettingEnabled = "ipAllowlist.enabled"
	// SettingInstallation is the installation list: CIDRs or addresses joined
	// with ",". Empty means that no one set a list.
	SettingInstallation = "ipAllowlist.installation"
)

// SettingDeclarations returns the declarations of the installation settings of
// the IP allowlist. The database layer holds each one.
func SettingDeclarations() []setting.Declaration {
	return []setting.Declaration{
		{
			Key: SettingEnabled, Level: setting.LevelInstallation, Kind: setting.KindBool, Default: "false",
			Secret: false, Database: true, StaticCredentialHost: false, Apply: setting.ApplyLive, Check: nil,
			// Turning the feature off lets each address in.
			ReducesSecurity: func(old, next string) bool { return old == "true" && next != "true" },
			Doc:             "Check the installation list on each console sign-in and each request with a credential.",
		},
		{
			Key: SettingInstallation, Level: setting.LevelInstallation, Kind: setting.KindString, Default: "",
			Secret: false, Database: true, StaticCredentialHost: false, Apply: setting.ApplyLive,
			Check: checkList,
			// Clearing the list lets each address in, and a list that admits
			// an address that the old list did not admit widens the access.
			ReducesSecurity: listReducesSecurity,
			Doc: fmt.Sprintf("The installation list: at most %d CIDRs or addresses, joined with a comma.",
				MaxEntries),
		},
	}
}

// listReducesSecurity reports whether the change of the installation list
// from old to next lets in an address that the old list refused: a clear of a
// list, or a next list with an entry that no entry of the old list covers.
// Fail closed: a value that does not decode counts as a reduction.
func listReducesSecurity(old, next string) bool {
	if old != "" && next == "" {
		return true
	}

	oldList, err := DecodeList(old)
	if err != nil {
		return true
	}

	nextList, err := DecodeList(next)
	if err != nil {
		return true
	}

	return !oldList.Covers(nextList)
}

// checkList accepts an empty value and a list that ParseList accepts. The
// reason names the entry that fails.
func checkList(value string) error {
	_, err := DecodeList(value)

	return err
}
