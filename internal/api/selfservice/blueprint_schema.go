package selfservice

import (
	"encoding/json"

	"opendustpan.org/opendustpan/internal/api/apidesc"
	"opendustpan.org/opendustpan/internal/policy"
	"opendustpan.org/opendustpan/internal/quota"
	"opendustpan.org/opendustpan/internal/selfservice"
	"opendustpan.org/opendustpan/pkg/job"
)

// keyDescription is the schema member that holds the text of a description.
const keyDescription = "description"

// blueprintTypes declares the types of the blueprint document: a name for
// each nested object, the closed value sets of the string types, and the
// untyped values. The schema of each type comes from its Go struct and its
// JSON tags, so a field name cannot drift from the server.
func blueprintTypes() []apidesc.Declaration {
	return []apidesc.Declaration{
		apidesc.Named[selfservice.Program]("SelfServiceProgram"),
		apidesc.Named[selfservice.Module]("SelfServiceModule"),
		apidesc.Named[selfservice.Component]("SelfServiceComponent"),
		apidesc.Named[selfservice.Platform]("SelfServicePlatform"),
		apidesc.Named[selfservice.Delivery]("SelfServiceDelivery"),
		apidesc.Named[selfservice.GitDelivery]("SelfServiceGitDelivery"),
		apidesc.Named[selfservice.Input]("SelfServiceInput"),
		apidesc.Named[selfservice.Choice]("SelfServiceChoice"),
		apidesc.Named[selfservice.Source]("SelfServiceSource"),
		apidesc.Named[selfservice.OutputRef]("SelfServiceOutputRef"),
		apidesc.Named[selfservice.Filter]("SelfServiceFilter"),
		apidesc.Named[selfservice.Operand]("SelfServiceOperand"),
		apidesc.Named[selfservice.Rule]("SelfServiceRule"),
		apidesc.Named[selfservice.PolicyRule]("SelfServicePolicyRule"),
		apidesc.Named[selfservice.QuotaRule]("SelfServiceQuotaRule"),
		// The enums are open, as every enum of the API is: a client keeps a value
		// that it does not know yet. The server accepts only the listed values.
		apidesc.DeclareOpenEnum[selfservice.InputType]("SelfServiceInputType",
			"string", "integer", "number", "boolean", "string-list"),
		apidesc.DeclareOpenEnum[policy.Effect]("SelfServicePolicyEffect", "allow", "approve", "deny"),
		apidesc.DeclareOpenEnum[quota.Kind]("SelfServiceQuotaKind", "units", "requests", "expr"),
		apidesc.DeclareOpenEnum[quota.Scope]("SelfServiceQuotaScope", "namespace", "requester"),
		apidesc.DeclareOpenEnum[selfservice.HomeKind]("SelfServiceHomeKind", "git", "opendustpan"),
		apidesc.DeclareOpenEnum[job.Delivery]("SelfServiceJobDelivery", "review-request", "direct-push"),
		// The shared declaration of any JSON value, as the inventory API
		// states it. A declaration of the same type must be equal everywhere.
		apidesc.Declare[json.RawMessage](map[string]any{keyDescription: "Any JSON value."}),
		apidesc.Declare[selfservice.ExprVersionName](map[string]any{
			"type":         "string",
			keyDescription: "The Expr version that compiled the blueprint. Set by the server; a draft that names it is refused.",
		}),
		apidesc.Declare[selfservice.ScalarValue](map[string]any{
			"type":         []any{"string", "number", "boolean"},
			keyDescription: "A JSON scalar: a string, a number or a boolean. The server refuses an object or an array.",
		}),
	}
}
