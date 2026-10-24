package pulumi_test

import (
	"net/http"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/api/pulumi"
	"opendustpan.org/opendustpan/internal/errcode"
	"opendustpan.org/opendustpan/pkg/secret"
	"opendustpan.org/opendustpan/pkg/stack"
)

// TestStackAndSecretErrorsCarryACode proves that the errors of the stacks and
// of the secrets answer their code, and that a time before the first state
// names that time as an argument.
func TestStackAndSecretErrorsCarryACode(t *testing.T) {
	t.Parallel()

	first := time.Date(2026, 10, 23, 9, 30, 0, 0, time.FixedZone("CEST", 2*60*60))

	checkErrorAnswers(t, []errorAnswer{
		{"invalid stack name", stack.ErrInvalidName, http.StatusBadRequest, errcode.StackInvalidName, nil},
		{"stack not found", stack.ErrNotFound, http.StatusNotFound, errcode.StackNotFound, nil},
		{"no state", stack.ErrNoState, http.StatusNotFound, errcode.StackNoState, nil},
		{
			"no state at a time, first state known", &stack.NoStateAtError{First: first}, http.StatusNotFound,
			errcode.StackNoStateAtTime,
			map[string]any{"first": "2026-10-23T07:30:00Z"},
		},
		{"no state at a time, no checkpoint", stack.ErrNoStateAt, http.StatusNotFound, errcode.StackNoState, nil},
		{
			"resource not found", stack.ErrResourceNotFound, http.StatusNotFound,
			errcode.StackResourceNotFound, nil,
		},
		{"has resources", stack.ErrHasResources, http.StatusBadRequest, errcode.StackHasResources, nil},
		{"already exists", stack.ErrAlreadyExists, http.StatusConflict, errcode.StackAlreadyExists, nil},
		{
			"other secrets provider", stack.ErrOtherSecretsProvider, http.StatusConflict,
			errcode.StackOtherSecretsProvider, nil,
		},
		{
			"key not configured", secret.ErrKeyNotConfigured, http.StatusInternalServerError,
			errcode.SecretKeyNotConfigured, nil,
		},
		{"secret too large", secret.ErrTooLarge, http.StatusRequestEntityTooLarge, errcode.SecretTooLarge, nil},
		{"decrypt", secret.ErrDecrypt, http.StatusBadRequest, errcode.SecretNotDecrypted, nil},
		{
			"console session on secrets", pulumi.ErrSessionSecrets, http.StatusForbidden,
			errcode.SecretConsoleSession, nil,
		},
	})
}
