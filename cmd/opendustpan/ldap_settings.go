package main

import (
	"errors"
	"fmt"

	"opendustpan.org/opendustpan/internal/authgate"
)

// The key and the flag of the setting of the failed sign-ins of one person
// from every source. The declaration of the setting is in internal/cli, so
// the flag, the variable and the file key stay in one place.
const (
	ldapSubjectFailureLimitKey  = "ldap.subjectFailureLimit"
	ldapSubjectFailureLimitFlag = "ldap-subject-failure-limit"
)

// errLDAPSubjectFailureLimit reports a limit outside the range of the
// setting.
var errLDAPSubjectFailureLimit = errors.New("the limit of the failed sign-ins of one person is not valid")

// checkLDAPSubjectFailureLimit returns an error that names the setting when
// limit is outside the range of the setting. The server checks the value at
// the start, so a running server holds a limit that the lockout accepts.
func checkLDAPSubjectFailureLimit(limit int64) error {
	if limit < authgate.MinSubjectFailureLimit || limit > authgate.MaxSubjectFailureLimit {
		return fmt.Errorf("%s: %w: %d, want %d to %d", ldapSubjectFailureLimitKey,
			errLDAPSubjectFailureLimit, limit, authgate.MinSubjectFailureLimit, authgate.MaxSubjectFailureLimit)
	}

	return nil
}
