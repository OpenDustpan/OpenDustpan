package errcode

// The codes of the area. A code is never removed and never changes its
// meaning.
const (
	// StateVersionBusy means that the server has no room for the body of a state version now. The caller tries again
	// after the Retry-After time.
	StateVersionBusy = "state-version.busy"
	// StateVersionBudgetTooSmall means that the body of the state version can never fit the body budget of the server.
	StateVersionBudgetTooSmall = "state-version.budget-too-small"
	// StateVersionNotFound means that the state version does not exist or is not finalized.
	StateVersionNotFound = "state-version.not-found"
	// StateVersionResourceNotFound means that the state version has no resource or value at the address or the path of
	// the request.
	StateVersionResourceNotFound = "state-version.resource-not-found"
	// StateVersionTooLarge means that the body or the outputs of the state version are over the size limit.
	StateVersionTooLarge = "state-version.too-large"
	// StateVersionChecksumMismatch means that the uploaded state does not match its declared MD5.
	StateVersionChecksumMismatch = "state-version.checksum-mismatch"
	// StateVersionInvalid means that the state version or its state is not valid.
	StateVersionInvalid = "state-version.invalid"
	// StateVersionLineageMismatch means that the lineage of the state differs from the lineage of the current state.
	StateVersionLineageMismatch = "state-version.lineage-mismatch"
	// StateVersionRevealPathTooLong means that the value path of the reveal is longer than maxBytes bytes.
	StateVersionRevealPathTooLong = "state-version.reveal-path-too-long"
	// StateVersionStaleSerial means that the serial of the state is not newer than the serial of the current state.
	StateVersionStaleSerial = "state-version.stale-serial"
	// StateVersionUploadDenied means that the upload capability of the state version is not valid, has expired, or the
	// state version takes no upload.
	StateVersionUploadDenied = "state-version.upload-denied"
)

// stateVersionSpecs are the codes of the state versions of the TFE-compatible API.
func stateVersionSpecs() []Spec {
	return []Spec{
		{
			Code:    StateVersionBusy,
			Meaning: "the server has no room for the body of a state version now; try again after the Retry-After time",
			Args:    nil,
		},
		{
			Code:    StateVersionBudgetTooSmall,
			Meaning: "the body of the state version can never fit the body budget of the server",
			Args:    nil,
		},
		{
			Code:    StateVersionNotFound,
			Meaning: "the state version does not exist or is not finalized",
			Args:    nil,
		},
		{
			Code:    StateVersionResourceNotFound,
			Meaning: "the state version has no resource or value at the address or the path of the request",
			Args:    nil,
		},
		{
			Code:    StateVersionTooLarge,
			Meaning: "the body or the outputs of the state version are over the size limit",
			Args:    nil,
		},
		{
			Code:    StateVersionChecksumMismatch,
			Meaning: "the uploaded state does not match its declared MD5",
			Args:    nil,
		},
		{
			Code:    StateVersionInvalid,
			Meaning: "the state version or its state is not valid",
			Args:    nil,
		},
		{
			Code:    StateVersionLineageMismatch,
			Meaning: "the lineage of the state differs from the lineage of the current state",
			Args:    nil,
		},
		{
			Code:    StateVersionRevealPathTooLong,
			Meaning: "the value path of the reveal is longer than maxBytes bytes",
			Args:    []Arg{{Name: argMaxBytes, Kind: KindQuantity}},
		},
		{
			Code:    StateVersionStaleSerial,
			Meaning: "the serial of the state is not newer than the serial of the current state",
			Args:    nil,
		},
		{
			Code:    StateVersionUploadDenied,
			Meaning: "the upload capability of the state version is not valid, has expired, or the state version takes no upload",
			Args:    nil,
		},
	}
}
