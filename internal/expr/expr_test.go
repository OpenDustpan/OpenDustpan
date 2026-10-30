package expr_test

import (
	"testing"

	"opendustpan.org/opendustpan/internal/catalog"
	"opendustpan.org/opendustpan/internal/catalog/catalogtest"
	"opendustpan.org/opendustpan/internal/expr"
)

func TestEvaluatorConformance(t *testing.T) {
	t.Parallel()

	catalogtest.RunEvaluator(t, func(*testing.T) catalog.Evaluator { return expr.New() })
}
