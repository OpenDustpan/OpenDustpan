package errcode

// RestoreBlobsMissing means that the data of a backup names blobs that the
// backup does not hold, so a restore of the backup would bring back rows with
// no content. The restore refuses the backup. The failure text of the restore
// carries the code in its row, for the operator.
const RestoreBlobsMissing = "restore.blobs-missing"

// restoreSpecs are the codes of the restores of the installation.
func restoreSpecs() []Spec {
	return []Spec{
		{
			Code:    RestoreBlobsMissing,
			Meaning: "the data of the backup names blobs that the backup does not hold, so the restore refused it",
			Args:    nil,
		},
	}
}
