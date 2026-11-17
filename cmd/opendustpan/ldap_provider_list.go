package main

import "opendustpan.org/opendustpan/internal/cli"

// ldapSubjectFailureLimitSource returns the layer that sets the limit of the
// failed sign-ins of one person from every source, and whether a layer sets
// it. A process with no resolved setting uses the default.
func ldapSubjectFailureLimitSource(resolved []cli.Resolved) (string, bool) {
	for _, r := range resolved {
		if r.Setting == nil || r.Setting.File != ldapSubjectFailureLimitKey {
			continue
		}

		return string(r.Source), r.Source != cli.SourceDefault
	}

	return string(cli.SourceDefault), false
}
