package errcode

// The codes of the first administrator. A code is never removed and never
// changes its meaning.
const (
	// FirstAdministratorAlreadyMade means that the first administrator is made already, so the offer and the redeem are
	// closed.
	FirstAdministratorAlreadyMade = "first-administrator.already-made"
	// FirstAdministratorCodeInvalid means that the code is wrong, spent, expired, or for another person.
	FirstAdministratorCodeInvalid = "first-administrator.code-invalid"
	// FirstAdministratorGrantExists means that the person holds the role already.
	FirstAdministratorGrantExists = "first-administrator.grant-exists"
	// FirstAdministratorPrincipalInvalid means that the principal cannot be the first administrator.
	FirstAdministratorPrincipalInvalid = "first-administrator.principal-invalid"
	// FirstAdministratorReasonInvalid means that the reason is blank, too long, not UTF-8, or holds a hidden character.
	FirstAdministratorReasonInvalid = "first-administrator.reason-invalid"
)

// firstAdministratorSpecs are the codes of the first administrator.
func firstAdministratorSpecs() []Spec {
	return []Spec{
		{
			Code: FirstAdministratorAlreadyMade,
			Meaning: "the server made the first administrator already, so the code route does not work. " +
				"To get an administrator again, run opendustpan host create-temp-admin on the server host",
			Args: nil,
		},
		{
			Code: FirstAdministratorCodeInvalid,
			Meaning: "the code is not valid: it can be wrong, used, expired, or for another person. " +
				"Ask the operator for a new code with POST /api/server/first-administrator",
			Args: nil,
		},
		{
			Code: FirstAdministratorGrantExists,
			Meaning: "the person holds the site-administrator role already. " +
				"The code stays unused, and the server does not mark the first administrator as made",
			Args: nil,
		},
		{
			Code: FirstAdministratorPrincipalInvalid,
			Meaning: "this principal cannot be the first administrator. " +
				"Use the ID of an enabled person of this installation, not a service, " +
				"a break-glass person, a temporary administrator, or a dev: user",
			Args: nil,
		},
		{
			Code: FirstAdministratorReasonInvalid,
			Meaning: "the reason is not valid. Write 1 to 500 characters of UTF-8 text " +
				"with no control character and no format character",
			Args: nil,
		},
	}
}
