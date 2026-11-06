package expr_test

import (
	"testing"

	"opendustpan.org/opendustpan/internal/catalog"
	"opendustpan.org/opendustpan/internal/catalog/catalogtest"
)

func TestEvaluatorConformance(t *testing.T) {
	t.Parallel()

	catalogtest.RunEvaluator(t, func(t *testing.T) catalog.Evaluator {
		t.Helper()

		return newEvaluator(t)
	})
}
