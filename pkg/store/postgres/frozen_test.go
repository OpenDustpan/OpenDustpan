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
		"0001_init.sql":                            "9b7354cc3f1dbedd3f8e3559e1770b7d5ce267e2014233332522fbeac62ec40f",
		"0002_updates.sql":                         "9589fc4dbfd9cd98b3b2a1e624c0b986081d0e123b91171ca3144e845564f6d4",
		"0003_stack_id.sql":                        "b859d2ec3bfe15466ecab4521a0c2cb7bb76fd7e1f8ccd7d5ca7ea57f3bcc724",
		"0004_updates_stack_id.sql":                "f7436ce58185b0065a15641cb7448f1dadce5118da10db69e63f9608dc59e3a9",
		"0005_updates_drop_names.sql":              "47fd896ec98b889118327d4ed1dcb3471c80fe52aeb6fac9b5f10768f5c64553",
		"0006_checkpoints_per_update.sql":          "277600f92cb5b8d9cc109045f30e4f3b587cedc10c7196e38729cbd7fe0eb034",
		"0007_stack_tags.sql":                      "1f7497d17d2bbea22bc89e683e368f4f84ba29edf9774329ad3adee256f44837",
		"0008_secret_keys.sql":                     "de34b1a2f9ff8f3438c99e79c39d8f0d4da581d4df8c6f54f384f8e72e919f07",
		"0009_inventory.sql":                       "dd55120732d2b1e747a2a564057c39894dea33849e6d9a1284894e138b7cbd79",
		"0010_workspaces.sql":                      "5e0bb9862882850c84f3a1709f611bfcf70252c60b80f9215d733b4993693e45",
		"0011_restored_from.sql":                   "32a8f5ef75af5407cf56308fe9ba5198d564da7caedea353ad470f89c8e42f4a",
		"0012_update_history_fields.sql":           "929d598c14a67c3d9ac278537f83eb23dbb4a92164eeef669fd31ae89aab0235",
		"0013_backups.sql":                         "9586a4e167c09ca919012fe269d44cc9183182823ff980949183d8060370df13",
		"0014_checkpoint_resource_count.sql":       "eef1ec99c03704c66f80929fda35cbe13edaed395847867b248cd34cdb66a2bf",
		"0015_maintenance.sql":                     "fe7f0910b40e911edd4599847fdc1e3a3f8b8c8cf1b601ab5b7eb8ae08c922a7",
		"0016_checkpoint_blobs.sql":                "b69c018c9b362ea4452ea7724a0ef6fac9d945fbcaddf90a1952d0b682ae57b9",
		"0017_inventory_lock.sql":                  "5db1e38161cfcdb399d64d4e8ec975f9295f2de907597819a84e02e6531b7df3",
		"0018_restores.sql":                        "ae1be8a786e9543fa51d88f8b2a709b3f42f18ce53b4642c365c94a858c93ed1",
		"0019_audit_entries.sql":                   "4e164d6d6d05f73b4c073558514ba2ac7c1e3c7527afb9bb7a890d80c6a15189",
		"0020_sealed_state.sql":                    "0e5358918cfed4540c40cc685142087d14b0133a6676ad19860fb407445eb52c",
		"0021_sensitive_marks.sql":                 "6657559a75930c75722d3cef37de2bb5dfc8fd4122e752f029054158c4fed8a2",
		"0022_audit_value_count.sql":               "cef0458c6909a4582a865fddae0e5e76c23c4852b6db438eb664c110766a65d4",
		"0023_update_journal_version.sql":          "659b5383d1bc1c5d7ab464462301ffbf1402bcfb160f4751360b1717a53b9d34",
		"0024_state_fence.sql":                     "b6c73962a987448e7feeda2622535df7bc2dd78ad31cbdf3dbf2a47b24572ef7",
		"0025_update_journal.sql":                  "5a10de0e2729fce5b5ebf539552cd79b826db3da9cc517954cc3e5385d9c31d2",
		"0026_override_switches.sql":               "3c28dbd3cd4a8a8c649bd5e840714e8720b5c0fe1b76d6cc9af49399a7d2e8ca",
		"0027_update_journal_md5.sql":              "98aa8b9630806bb23a41f0c0d159f088e704876bf144f4b88588ceac62858327",
		"0028_principals_and_groups.sql":           "c9eaa057e820a0e119743f103b8ece304730f7e26c1b92541f1ef77a7f653af7",
		"0029_audit_server_scope.sql":              "4849a1d99647a8047e3bcdec9c5f2658a520613a68fd59173dc5f17e89d73a61",
		"0031_soft_delete_stacks.sql":              "7b721532e4f8aba05309f36981994a5b50d4ad7fba6cd6f2d7ef8e0d2b62edb3",
		"0032_soft_delete_workspaces.sql":          "cd937bcc20655f7fcb92b56451187b6d81d220c5789ff89634c85afb55bdf5f6",
		"0035_access_tokens.sql":                   "a24b9b2f77ad6ec30547c0254c2d053aab91646afd887e310b38fca852559103",
		"0042_roles_and_grants.sql":                "d77c4971fb1f444c4f902e3d01413751cefe75a8a8f3b77132a0bf09f7be3297",
		"0050_update_token_id.sql":                 "f162701f6dc2931b27a1fbd5e247b6b0962193b6557b52c2ffe7e04005f2ae9f",
		"0048_audit_changes.sql":                   "4a4b1cba2d407ebd986c54a405d025186ae6b9f5b867cc2a5333e8f54e825cec",
		"0049_audit_sink_cursors.sql":              "65cee05080eec18e931ff91a7847e98b6f3f7cf4ed42e4afa4b66d956d077418",
		"0054_token_policy.sql":                    "9a36e62b1ce6943d8ec762ce1732ea2b78e56eb2ff5c017a9ddde539786577c8",
		"0056_audit_object_id_and_blob_purges.sql": "6f48b62438b91099e44d3de20a6905ba8d9b1ca90538382a9c7f2528ddf2fad2",
		"0057_namespace_ip_allowlist.sql":          "24567a6a4104e14e1909dfeb3489cc7fafebcded41fd1672980b3877006ba051",
		"0058_inventory_deleted_purge_after.sql":   "a11abf98a8894509350b3c193f00ebe3bb5809f32b8ab424cbad5cf5b70ba2fb",
		"0059_collections.sql":                     "d6aba083e67c43c5df37b823ac0535d835f3da819099e5e9cde0bf9d57e1c97d",
		"0062_sessions.sql":                        "7a6c50236de51f453ad519a75dc1966b87a2b8d189b8366160df41624d251457",
		"0064_blob_purges_age_byte_order.sql":      "e2a6965f07096da54f1e00240016a349cb046571b05a3ddec0544ec593c86d2d",
		"0065_session_policy.sql":                  "851a98fd2a4cbc0bd13d1365a9639a3208962870782e2cf7b262d4782bd01112",
		"0066_namespace_principals.sql":            "1f8013ffa3162a5bcaf313ef65dbd74de34b728bcb24a2f15c34e930d0201923",
		"0067_audit_chain_state.sql":               "1c23fdc4de99e2a7a5d10a3aa71446582c2ec56738394867be8840df4faadfb0",
		"0068_update_journal_records_changed.sql":  "41672a0c124301d6a3448a3c5171dcb978d95fd5332696c6ff305b2d3608ecb9",
		"0069_audit_sink_leases.sql":               "e506a8096d0bb5a0d756ecfbba498b8e868f3c26341ee92a08fcdca01485e7aa",
		"0070_membership_source_state.sql":         "12a709490400f15e186f2fe3c8647be99c75858cce493c9d0e6738070f17a81d",
		"0071_break_glass_accounts.sql":            "4808b43c4492416709520b3c868185084791694c078eb1b59c05dbc1ed7a8aa3",
		"0072_temp_admins.sql":                     "f4cf80c4a4cdf2f388119c14db0d0643c705be4948d7eacada8f699e28e8fe0b",
		"0074_update_journal_refusals.sql":         "5b7c61626ef1913f4aba46a199c9ca52fe60e01aa37bea2ebc041358b977bc91",
		"0075_sign_in_freshness.sql":               "7f1da42551a23fd6aa6a3cd8ea019a217443a8de4d5220fdee5af4da6e41a4cd",
		"0076_sign_in_providers.sql":               "cccf0a9cfd03c8b6696699a8de69425a7964f472337dc4f0790452b5bbc03c70",
		"0078_one_running_backup_and_restore.sql":  "480ae3db44d6bced8a266f7b6088b04cc954e848bcd6989bd989746fe5e6c7ff",
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
