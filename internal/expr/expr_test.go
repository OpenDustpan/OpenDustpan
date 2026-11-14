package expr_test

import (
	"testing"

	"opendustpan.org/opendustpan/internal/selfservice"
	"opendustpan.org/opendustpan/internal/selfservice/selfservicetest"
)

func TestEvaluatorConformance(t *testing.T) {
	t.Parallel()

	selfservicetest.RunEvaluator(t, func(t *testing.T) selfservice.Evaluator {
		t.Helper()

		return newPatientEvaluator(t)
	})
}
