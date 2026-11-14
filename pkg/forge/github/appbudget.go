package github

import (
	"fmt"

	"opendustpan.org/opendustpan/pkg/forge"
)

// spendApp takes one call from the budget of the app, before a request that
// the key of the app signs. When the budget is spent, it returns
// forge.ErrAppBudget and forge.ErrUnavailable with the time to wait, and the
// caller makes no request. All connections of the app share the budget: the key names the
// API address and the app ID, not the installation.
func (f *gitHub) spendApp() error {
	if wait, ok := f.o.budget.Spend(f.base+"#"+f.appID, f.o.now()); !ok {
		spent := forge.Unavailable(wait, "the app made as many signed calls as its budget allows")

		return fmt.Errorf("%w: %w", forge.ErrAppBudget, spent)
	}

	return nil
}
