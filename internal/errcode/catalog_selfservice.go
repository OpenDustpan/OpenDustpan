package errcode

// The codes of the blueprints of the self-service API. A code is never
// removed and never changes its meaning.
const (
	// SelfServiceBlueprintNotFound means that the namespace has no blueprint
	// with the name, or no version of it.
	SelfServiceBlueprintNotFound = "self-service.blueprint-not-found"
	// SelfServiceBlueprintInvalid means that the blueprint is not valid.
	SelfServiceBlueprintInvalid = "self-service.blueprint-invalid"
	// SelfServiceBlueprintTooLarge means that the blueprint document is larger
	// than the limit.
	SelfServiceBlueprintTooLarge = "self-service.blueprint-too-large"
	// SelfServiceBlueprintStale means that the draft changed since it was read,
	// or that the If-Match header is missing.
	SelfServiceBlueprintStale = "self-service.blueprint-stale"
	// SelfServiceBlueprintAlreadyPublished means that this draft version is
	// already published.
	SelfServiceBlueprintAlreadyPublished = "self-service.blueprint-already-published"
	// SelfServiceBlueprintLimit means that the namespace holds the largest
	// number of blueprints or versions.
	SelfServiceBlueprintLimit = "self-service.blueprint-limit"
)

// selfServiceSpecs are the codes of the blueprints of the self-service API.
// No code of the area has arguments.
func selfServiceSpecs() []Spec {
	meanings := [][2]string{
		{
			SelfServiceBlueprintNotFound,
			"the namespace has no blueprint with the name, or no version of it",
		},
		{SelfServiceBlueprintInvalid, "the blueprint is not valid"},
		{SelfServiceBlueprintTooLarge, "the blueprint document is larger than the limit"},
		{
			SelfServiceBlueprintStale,
			"the draft changed since it was read, or the If-Match header is missing",
		},
		{
			SelfServiceBlueprintAlreadyPublished,
			"this draft version is already published: change the draft first",
		},
		{SelfServiceBlueprintLimit, "the namespace holds the largest number of blueprints or versions"},
	}

	out := make([]Spec, 0, len(meanings))
	for _, m := range meanings {
		out = append(out, Spec{Code: m[0], Meaning: m[1], Args: nil})
	}

	return out
}
