package errcode

// The codes of the area. A code is never removed and never changes its
// meaning.
const (
	// StoreFailoverSettingInvalid means that a failover setting of the database URL is outside its allowlist,
	// so the server does not start.
	StoreFailoverSettingInvalid = "store.failover-setting-invalid"
	// StoreServerVersionUnsupported means that the PostgreSQL server has a major version below the minimum,
	// or a version that cannot be read, so the server does not start.
	StoreServerVersionUnsupported = "store.server-version-unsupported"
)

// storeSpecs are the codes of the store adapters.
func storeSpecs() []Spec {
	return []Spec{
		{
			Code:    StoreFailoverSettingInvalid,
			Meaning: "a failover setting of the database URL is outside its allowlist, so the server does not start",
			Args:    []Arg{{Name: "setting", Kind: KindIdentifier}},
		},
		{
			Code:    StoreServerVersionUnsupported,
			Meaning: "the PostgreSQL server has a major version below the minimum, or a version that cannot be read, so the server does not start",
			Args:    nil,
		},
	}
}
