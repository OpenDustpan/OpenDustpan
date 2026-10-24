package errcode

// BackupCorrupt is a backup whose stored data the server cannot use, for
// example a manifest that is missing, cut or of another format.
const BackupCorrupt = "backup.corrupt"

// backupSpecs are the codes of the backups of the installation.
func backupSpecs() []Spec {
	return []Spec{
		{
			Code:    BackupCorrupt,
			Meaning: "the stored data of the backup is missing, cut or does not match its manifest",
			Args:    []Arg{{Name: "backup", Kind: KindIdentifier}},
		},
	}
}
