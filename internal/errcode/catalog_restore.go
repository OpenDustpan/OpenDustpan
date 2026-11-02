package errcode

// RestoreBlobsMissing means that the data of a backup names blobs that the
// backup does not hold, so a restore of the backup would bring back rows with
// no content. The restore refuses the backup. The failure text of the restore
// carries the code in its row, for the operator.
const RestoreBlobsMissing = "restore.blobs-missing"

// RestoreLiveBlobsDamaged means that the backup before a restore found live
// blobs whose bytes do not match their records or do not open. A restore
// never writes over a live blob, so it stopped before any change of the live
// data. The failure text of the restore names the keys.
const RestoreLiveBlobsDamaged = "restore.live-blobs-damaged"

// RestoreLiveBlobsMissing means that the backup before a restore found more
// live blobs missing than the bound of the restore allows. The restore
// stopped before any change of the live data.
const RestoreLiveBlobsMissing = "restore.live-blobs-missing"

// RestorePreBackupForeign means that the backup before a restore is not one
// that the restore started. The restore stopped before any change of the
// live data.
const RestorePreBackupForeign = "restore.pre-backup-foreign"

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
			Code:    RestoreLiveBlobsDamaged,
			Meaning: "the backup before the restore found live blobs that are damaged, so the restore changed nothing",
			Args:    nil,
		},
		{
			Code:    RestoreLiveBlobsMissing,
			Meaning: "the backup before the restore found too many live blobs missing, so the restore changed nothing",
			Args:    nil,
		},
		{
			Code:    RestorePreBackupForeign,
			Meaning: "the backup before the restore is not one that the restore started, so it changed nothing",
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
