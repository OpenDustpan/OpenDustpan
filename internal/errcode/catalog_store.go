package errcode

// The codes of the area. A code is never removed and never changes its
// meaning.
const (
	// StoreFailoverSettingInvalid means that a failover setting of the database URL is outside its allowlist,
	// so the server does not start.
	StoreFailoverSettingInvalid = "store.failover-setting-invalid"
)

// storeSpecs are the codes of the store adapters.
func storeSpecs() []Spec {
	return []Spec{
		{
			Code:    StoreFailoverSettingInvalid,
			Meaning: "a failover setting of the database URL is outside its allowlist, so the server does not start",
			Args:    []Arg{{Name: "setting", Kind: KindIdentifier}},
		},
	}
}
