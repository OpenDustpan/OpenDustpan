package postgres_test

import (
	"testing"

	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/profile/profiletest"
)

// PutRestoreOpenSession implements the open session case of the restore
// suite: it makes a namespace with a profile and a live session.
func (s *backupStore) PutRestoreOpenSession(t *testing.T, ns namespace.Name) string {
	t.Helper()

	return profiletest.SeedOpenSession(t, s.sessionPorts(), ns.String())
}

// HasRestoreOpenSession implements the open session case of the restore
// suite.
func (s *backupStore) HasRestoreOpenSession(t *testing.T, ns namespace.Name, id string) bool {
	t.Helper()

	return profiletest.HasOpenSession(t, s.sessionPorts(), ns.String(), id)
}

func (s *backupStore) sessionPorts() profiletest.Store {
	return profiletest.Store{
		Profiles: s.Profiles(), Namespaces: s.Store, Audit: s.Store, Records: nil,
		Sessions: s.ProfileSessions(),
	}
}
