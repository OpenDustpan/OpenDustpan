package catalog

import (
	"fmt"

	"opendustpan.org/opendustpan/pkg/job"
)

// checkRepository returns an error that wraps ErrInvalid for a repository
// that job.CheckRepository refuses: one that is neither a path, such as
// acme/infra, nor an https URL with a host and no user name, password, query
// or fragment. what names the field in the error. The error never holds the
// repository, because a refused repository can hold a credential.
//
// Defense in depth: the start of a job checks the repository of its code
// location again. This check stays so that a request document or a blueprint
// that holds a credential is refused before it is stored, and so that a new
// caller of the documents cannot skip the check.
func checkRepository(what, r string) error {
	if err := job.CheckRepository(r); err != nil {
		return fmt.Errorf("%w: the repository of %s must be a path such as acme/infra or an https URL with a "+
			"host and no user name, password, query or fragment", ErrInvalid, what)
	}

	return nil
}
