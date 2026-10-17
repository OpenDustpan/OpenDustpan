package sqlite

import (
	"context"
	"fmt"

	"opendustpan.org/opendustpan/pkg/access"
	"opendustpan.org/opendustpan/pkg/principal"
)

var _ access.ServicePrincipals = (*Store)(nil)

// ListServicePrincipals implements access.ServicePrincipals. It pages the
// service principals of the installation and of every namespace in subject
// order.
func (s *Store) ListServicePrincipals(ctx context.Context) ([]string, error) {
	var out []string

	after := ""

	for {
		rows, _, err := s.ListPrincipals(
			ctx,
			principal.Query{After: after, Low: "", High: "", Limit: principal.MaxLimit},
			principal.KindService,
		)
		if err != nil {
			return nil, fmt.Errorf("list service principals: %w", err)
		}

		if len(rows) == 0 {
			return out, nil
		}

		for i := range rows {
			out = append(out, rows[i].ID)
		}

		if len(rows) < principal.MaxLimit {
			return out, nil
		}

		after = rows[len(rows)-1].Subject
	}
}
