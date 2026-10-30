package blob

import "strings"

// StoreCheckDir is the top-level directory under which the store check of the
// installation writes its throwaway objects, one directory for each check.
const StoreCheckDir = "store-checks/"

// StoreCheckKey reports whether key lies under StoreCheckDir. A reaper of
// garbage must skip such a key: a blob that no row names is garbage, but the
// objects of a store check never have a row, and the prefix reaper of the
// store check deletes them.
func StoreCheckKey(key Key) bool {
	return strings.HasPrefix(key.String(), StoreCheckDir)
}
