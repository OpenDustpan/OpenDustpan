package setting

// SettingFreshAuthEnabled is the key of the installation setting that turns the
// fresh authentication of the approvals and the settings on and off (ADR 385).
const SettingFreshAuthEnabled = "freshAuthEnabled"

// valueFalse is the stored text of a bool setting that is off.
const valueFalse = "false"

// FreshAuthEnabledDeclaration returns the declaration of freshAuthEnabled. Its
// effective default is false. A change from on to off is a weaker change, and
// the change needs a fresh step while the setting is on. A change from off to
// on needs none.
func FreshAuthEnabledDeclaration() Declaration {
	return Declaration{
		Key: SettingFreshAuthEnabled, Level: LevelInstallation, Kind: KindBool, Default: valueFalse,
		Secret: false, Database: true, StaticCredentialHost: false, Apply: ApplyLive, Check: nil,
		// Fail closed: a value other than "false" counts as on, so the change to
		// "false" is a weaker change.
		ReducesSecurity: func(old, next string) bool { return old != valueFalse && next == valueFalse },
		Doc: "Ask a person for a fresh authentication at each step of the fresh-authentication window. " +
			"Off lets an access token with the grant approve a destroy, and makes a setting change weaker.",
	}
}
