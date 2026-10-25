package dustpan

import "context"

// LeftGroupSchemas lets the TestMain of the black-box tests list the schemas
// that the group directory tests left on the database at base.
func LeftGroupSchemas(ctx context.Context, base string) ([]string, error) {
	return leftGroupSchemas(ctx, base)
}
