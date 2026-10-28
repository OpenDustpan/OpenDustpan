package errcode

// RestoreBlobsMissing means that the data of a backup names blobs that the
// backup does not hold, so a restore of the backup would bring back rows with
// no content. The restore refuses the backup. The failure text of the restore
// carries the code in its row, for the operator.
const RestoreBlobsMissing = "restore.blobs-missing"

// RestoreSwapped means that a cancel came after the swap of the restore: the
// restored data is live, and the restore runs to its end.
const RestoreSwapped = "restore.swapped"

// RestoreEnded means that a cancel came after the end of the restore.
const RestoreEnded = "restore.ended"

// restoreSpecs are the codes of the restores of the installation.
func restoreSpecs() []Spec {
	return []Spec{
		{
			Code:    RestoreBlobsMissing,
			Meaning: "the data of the backup names blobs that the backup does not hold, so the restore refused it",
			Args:    nil,
		},
		{
			Code:    RestoreSwapped,
			Meaning: "the restore already swapped the data, so a cancel cannot stop it",
			Args:    []Arg{{Name: "restore", Kind: KindIdentifier}},
		},
		{
			Code:    RestoreEnded,
			Meaning: "the restore already ended, so a cancel has nothing to stop",
			Args:    []Arg{{Name: "restore", Kind: KindIdentifier}},
		},
	}
}
