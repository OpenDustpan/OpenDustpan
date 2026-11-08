package server

import (
	"net/http"

	"opendustpan.org/opendustpan/internal/api/apidesc"
	"opendustpan.org/opendustpan/pkg/backup"
)

// The tags of the backup and restore operations.
const (
	tagBackups  = "backups"
	tagRestores = "restores"
)

// backupOperationLabels names the backup and restore routes in the
// description. The start of a backup, the start of a restore and the cancel
// of a restore answer 202: the work runs in the background.
func backupOperationLabels() map[string]apidesc.Label {
	return map[string]apidesc.Label{
		apidesc.Pattern(http.MethodPost, backupsPath): apidesc.Name("startBackup", tagBackups).
			WithStatus(http.StatusAccepted).
			On(apidesc.KindServer).
			WithBodies(apidesc.NoBody, apidesc.Typed[startBackupResponse]("StartBackupResponse")),
		apidesc.Pattern(http.MethodGet, backupsPath): apidesc.Name("listBackups", tagBackups).
			On(apidesc.KindServer).
			WithBodies(apidesc.NoBody, apidesc.Typed[listBackupsResponse](
				"ListBackupsResponse", apidesc.Named[backupSummaryJSON]("BackupSummary"),
			)),
		apidesc.Pattern(http.MethodGet, backupPath): apidesc.Name("getBackup", tagBackups).
			On(apidesc.KindServer).
			WithBodies(apidesc.NoBody, apidesc.Typed[getBackupResponse](
				"GetBackupResponse", apidesc.Named[backup.Manifest]("BackupManifest"),
			)),
		apidesc.Pattern(http.MethodGet, backupCheckPath): apidesc.Name("checkBackup", tagBackups).
			On(apidesc.KindServer).
			WithBodies(apidesc.NoBody, apidesc.Typed[checkBackupResponse]("CheckBackupResponse")),
		apidesc.Pattern(http.MethodPost, restoresPath): apidesc.Name("startRestore", tagRestores).
			WithStatus(http.StatusAccepted).
			On(apidesc.KindServer).
			WithBodies(
				apidesc.Typed[startRestoreRequest]("StartRestoreRequest"),
				apidesc.Typed[startRestoreResponse](
					"StartRestoreResponse",
					apidesc.Named[restoreWarningJSON]("RestoreWarning"),
					apidesc.Named[missingBlobLimitJSON]("MissingBlobLimit"),
				),
			),
		apidesc.Pattern(http.MethodGet, restoresPath): apidesc.Name("listRestores", tagRestores).
			On(apidesc.KindServer).
			WithBodies(apidesc.NoBody, apidesc.Typed[listRestoresResponse](
				"ListRestoresResponse",
				apidesc.Named[restoreSummaryJSON]("RestoreSummary"),
				apidesc.Named[restoreWarningJSON]("RestoreWarning"),
			)),
		apidesc.Pattern(http.MethodGet, restorePath): apidesc.Name("getRestore", tagRestores).
			On(apidesc.KindServer).
			WithBodies(apidesc.NoBody, apidesc.Typed[getRestoreResponse](
				"GetRestoreResponse",
				apidesc.Named[backup.Manifest]("BackupManifest"),
				apidesc.Named[restoreWarningJSON]("RestoreWarning"),
				apidesc.Named[devDataJSON]("DevData"),
				apidesc.Named[blobFindingsJSON]("BlobFindings"),
				apidesc.Named[missingBlobLimitJSON]("MissingBlobLimit"),
			)),
		apidesc.Pattern(http.MethodPost, restoreCancelPath): apidesc.Name("cancelRestore", tagRestores).
			WithStatus(http.StatusAccepted).
			On(apidesc.KindServer).
			WithBodies(apidesc.NoBody, apidesc.Typed[cancelRestoreResponse]("CancelRestoreResponse")),
	}
}
