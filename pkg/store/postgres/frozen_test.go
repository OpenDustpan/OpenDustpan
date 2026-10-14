package postgres_test

import (
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"testing"

	"opendustpan.org/opendustpan/pkg/store/internal/migrate"
)

//go:embed migrations/*.sql
var frozenMigrations embed.FS

// TestFrozenMigrations holds the SHA-256 of each released migration file. It
// fails when a file changes, when a file has no hash entry, and when a hash
// entry has no file. Never edit an applied migration. Add a new numbered file
// and its hash in the same change.
func TestFrozenMigrations(t *testing.T) {
	t.Parallel()

	// wantFrozenHashes holds the SHA-256 of each released migration file. A new
	// migration adds its hash here in the same change.
	//nolint:gosec // SHA-256 hashes of migration files, not credentials.
	wantFrozenHashes := map[string]string{
		"0001_init.sql":                      "9b7354cc3f1dbedd3f8e3559e1770b7d5ce267e2014233332522fbeac62ec40f",
		"0002_updates.sql":                   "9589fc4dbfd9cd98b3b2a1e624c0b986081d0e123b91171ca3144e845564f6d4",
		"0003_stack_id.sql":                  "b859d2ec3bfe15466ecab4521a0c2cb7bb76fd7e1f8ccd7d5ca7ea57f3bcc724",
		"0004_updates_stack_id.sql":          "f7436ce58185b0065a15641cb7448f1dadce5118da10db69e63f9608dc59e3a9",
		"0005_updates_drop_names.sql":        "47fd896ec98b889118327d4ed1dcb3471c80fe52aeb6fac9b5f10768f5c64553",
		"0006_checkpoints_per_update.sql":    "277600f92cb5b8d9cc109045f30e4f3b587cedc10c7196e38729cbd7fe0eb034",
		"0007_stack_tags.sql":                "1f7497d17d2bbea22bc89e683e368f4f84ba29edf9774329ad3adee256f44837",
		"0008_secret_keys.sql":               "de34b1a2f9ff8f3438c99e79c39d8f0d4da581d4df8c6f54f384f8e72e919f07",
		"0009_inventory.sql":                 "dd55120732d2b1e747a2a564057c39894dea33849e6d9a1284894e138b7cbd79",
		"0010_workspaces.sql":                "5e0bb9862882850c84f3a1709f611bfcf70252c60b80f9215d733b4993693e45",
		"0011_restored_from.sql":             "32a8f5ef75af5407cf56308fe9ba5198d564da7caedea353ad470f89c8e42f4a",
		"0012_update_history_fields.sql":     "929d598c14a67c3d9ac278537f83eb23dbb4a92164eeef669fd31ae89aab0235",
		"0013_backups.sql":                   "9586a4e167c09ca919012fe269d44cc9183182823ff980949183d8060370df13",
		"0014_checkpoint_resource_count.sql": "eef1ec99c03704c66f80929fda35cbe13edaed395847867b248cd34cdb66a2bf",
		"0015_maintenance.sql":               "fe7f0910b40e911edd4599847fdc1e3a3f8b8c8cf1b601ab5b7eb8ae08c922a7",
		"0016_checkpoint_blobs.sql":          "b69c018c9b362ea4452ea7724a0ef6fac9d945fbcaddf90a1952d0b682ae57b9",
		"0017_inventory_lock.sql":            "5db1e38161cfcdb399d64d4e8ec975f9295f2de907597819a84e02e6531b7df3",
		"0018_restores.sql":                  "ae1be8a786e9543fa51d88f8b2a709b3f42f18ce53b4642c365c94a858c93ed1",
		"0019_audit_entries.sql":             "4e164d6d6d05f73b4c073558514ba2ac7c1e3c7527afb9bb7a890d80c6a15189",
		"0020_sealed_state.sql":              "0e5358918cfed4540c40cc685142087d14b0133a6676ad19860fb407445eb52c",
		"0021_sensitive_marks.sql":           "6657559a75930c75722d3cef37de2bb5dfc8fd4122e752f029054158c4fed8a2",
		"0022_audit_value_count.sql":         "cef0458c6909a4582a865fddae0e5e76c23c4852b6db438eb664c110766a65d4",
		"0023_update_journal_version.sql":    "659b5383d1bc1c5d7ab464462301ffbf1402bcfb160f4751360b1717a53b9d34",
		"0024_state_fence.sql":               "b6c73962a987448e7feeda2622535df7bc2dd78ad31cbdf3dbf2a47b24572ef7",
		"0025_update_journal.sql":            "5a10de0e2729fce5b5ebf539552cd79b826db3da9cc517954cc3e5385d9c31d2",
		"0026_override_switches.sql":         "3c28dbd3cd4a8a8c649bd5e840714e8720b5c0fe1b76d6cc9af49399a7d2e8ca",
		"0027_update_journal_md5.sql":        "98aa8b9630806bb23a41f0c0d159f088e704876bf144f4b88588ceac62858327",
		"0028_principals_and_groups.sql":     "c9eaa057e820a0e119743f103b8ece304730f7e26c1b92541f1ef77a7f653af7",
		"0029_audit_server_scope.sql":        "4849a1d99647a8047e3bcdec9c5f2658a520613a68fd59173dc5f17e89d73a61",
		"0031_soft_delete_stacks.sql":        "7b721532e4f8aba05309f36981994a5b50d4ad7fba6cd6f2d7ef8e0d2b62edb3",
		"0032_soft_delete_workspaces.sql":    "cd937bcc20655f7fcb92b56451187b6d81d220c5789ff89634c85afb55bdf5f6",
		"0035_access_tokens.sql":             "a24b9b2f77ad6ec30547c0254c2d053aab91646afd887e310b38fca852559103",
		"0038_audit_sink_cursors.sql":        "65cee05080eec18e931ff91a7847e98b6f3f7cf4ed42e4afa4b66d956d077418",
	}

	all, err := migrate.Load(frozenMigrations, "migrations")
	if err != nil {
		t.Fatalf("load embedded migrations: %v", err)
	}

	for _, m := range all {
		want, ok := wantFrozenHashes[m.Name]
		if !ok {
			t.Errorf(
				"migration %s has no hash entry: add its hash in TestFrozenMigrations; "+
					"never edit an applied migration, add a new numbered file for a change",
				m.Name,
			)

			continue
		}

		sum := sha256.Sum256([]byte(m.SQL))
		if got := hex.EncodeToString(sum[:]); got != want {
			t.Errorf(
				"migration %s hash = %s, want %s: "+
					"never edit an applied migration; add a new numbered file and its hash",
				m.Name,
				got,
				want,
			)
		}
	}

	for name := range wantFrozenHashes {
		found := false

		for _, m := range all {
			if m.Name == name {
				found = true

				break
			}
		}

		if !found {
			t.Errorf(
				"hash entry %s has no migration file: "+
					"never edit an applied migration; add a new numbered file and its hash",
				name,
			)
		}
	}
}
