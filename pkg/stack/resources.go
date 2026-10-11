package stack

import (
	"encoding/json"
)

// rootStackType is the type of the automatic stack resource. A deployment
// with only this resource holds no managed resources.
const rootStackType = "pulumi:pulumi:Stack"

// deploymentResources is the part of a checkpoint deployment that the
// resource count reads: the list of managed resources.
type deploymentResources struct {
	Resources []deploymentResource `json:"resources"`
}

// deploymentResource is one entry of the resources list in a checkpoint
// deployment, as far as the resource count reads it.
type deploymentResource struct {
	Type string `json:"type"`
}

// CountManagedResources counts the resources in deployment beyond the
// automatic stack resource. A deployment that does not parse counts as one
// managed resource, so a delete without force cannot drop state the server
// cannot read.
func CountManagedResources(deployment []byte) int64 {
	var doc deploymentResources

	if err := json.Unmarshal(deployment, &doc); err != nil {
		return 1
	}

	var n int64

	for i := range doc.Resources {
		if doc.Resources[i].Type != rootStackType {
			n++
		}
	}

	return n
}
