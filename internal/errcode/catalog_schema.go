package errcode

// The codes of the area. A code is never removed and never changes its
// meaning.
const (
	// SchemaBinaryBelowMinimum is a write of a server whose binary version is below the minimum binary version
	// of the database schema.
	SchemaBinaryBelowMinimum = "schema.binary-below-minimum"
)

// schemaSpecs are the codes of the database schema.
func schemaSpecs() []Spec {
	return []Spec{
		{
			Code: SchemaBinaryBelowMinimum,
			Meaning: "the binary version of this server is below the minimum binary version of the database schema, " +
				"so this server cannot write",
			Args: []Arg{
				{Name: "binaryVersion", Kind: KindIdentifier},
				{Name: "minimumVersion", Kind: KindIdentifier},
			},
		},
	}
}
