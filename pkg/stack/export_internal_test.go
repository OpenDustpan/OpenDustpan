package stack

import "opendustpan.org/opendustpan/pkg/outputshare"

// ParseRootOutputs returns the root outputs of deployment as the Outputs port
// reads them, so the external tests reach the parse without a store.
func ParseRootOutputs(deployment []byte) ([]outputshare.Output, error) {
	return rootOutputs(deployment)
}
