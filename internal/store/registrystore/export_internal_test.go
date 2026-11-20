package registrystore

import "opendustpan.org/opendustpan/pkg/registry"

// DecodeArchivesJSON exposes decodeArchives to the black-box tests, so that a
// test can send a hostile row to the decode alone.
func DecodeArchivesJSON(s string) ([]registry.Archive, error) {
	return decodeArchives(s)
}
