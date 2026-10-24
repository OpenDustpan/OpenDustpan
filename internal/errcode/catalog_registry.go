package errcode

// The codes of the area. A code is never removed and never changes its
// meaning.
const (
	// RegistryArchiveSetRefused means that the release check refused the set of archives of the release.
	RegistryArchiveSetRefused = "registry.archive-set-refused"
	// RegistryInvalid means that the registry request is not valid.
	RegistryInvalid = "registry.invalid"
	// RegistryKeyExists means that the signing key exists.
	RegistryKeyExists = "registry.key-exists"
	// RegistryKeyInvalid means that the signing key is not valid.
	RegistryKeyInvalid = "registry.key-invalid"
	// RegistryKeyLimit means that the namespace has the largest number of signing keys.
	RegistryKeyLimit = "registry.key-limit"
	// RegistryNoKeys means that the namespace has no signing key to check the release.
	RegistryNoKeys = "registry.no-keys"
	// RegistryNotFound means that the registry object does not exist.
	RegistryNotFound = "registry.not-found"
	// RegistryNotStaged means that the release has no staged upload.
	RegistryNotStaged = "registry.not-staged"
	// RegistrySignatureInvalid means that the signature of the release is not valid.
	RegistrySignatureInvalid = "registry.signature-invalid"
	// RegistrySignatureUntrustedKey means that a key that the namespace does not trust signed the release.
	RegistrySignatureUntrustedKey = "registry.signature-untrusted-key"
	// RegistryTooLarge means that the upload is over the size limit.
	RegistryTooLarge = "registry.too-large"
	// RegistryVersionExists means that the version of the release exists.
	RegistryVersionExists = "registry.version-exists"
)

// registrySpecs are the codes of the private registry.
func registrySpecs() []Spec {
	return []Spec{
		{
			Code:    RegistryArchiveSetRefused,
			Meaning: "the release check refused the set of archives of the release",
			Args:    nil,
		},
		{Code: RegistryInvalid, Meaning: "the registry request is not valid", Args: nil},
		{Code: RegistryKeyExists, Meaning: "the signing key exists", Args: nil},
		{Code: RegistryKeyInvalid, Meaning: "the signing key is not valid", Args: nil},
		{Code: RegistryKeyLimit, Meaning: "the namespace has the largest number of signing keys", Args: nil},
		{Code: RegistryNoKeys, Meaning: "the namespace has no signing key to check the release", Args: nil},
		{Code: RegistryNotFound, Meaning: "the registry object does not exist", Args: nil},
		{Code: RegistryNotStaged, Meaning: "the release has no staged upload", Args: nil},
		{Code: RegistrySignatureInvalid, Meaning: "the signature of the release is not valid", Args: nil},
		{
			Code:    RegistrySignatureUntrustedKey,
			Meaning: "a key that the namespace does not trust signed the release",
			Args:    nil,
		},
		{Code: RegistryTooLarge, Meaning: "the upload is over the size limit", Args: nil},
		{Code: RegistryVersionExists, Meaning: "the version of the release exists", Args: nil},
	}
}
