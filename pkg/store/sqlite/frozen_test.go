package sqlite_test

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
		"0001_init.sql":                            "0699fd139cf6989a62bc44ec7c8418ade0104e744a8c0fffd0bf7659ea52da87",
		"0002_updates.sql":                         "1565e77abe88c21825b90566eee24123540c60ed5ff71b31602faf52d3faefee",
		"0003_stack_id.sql":                        "4f1a16b02f66d6fca5a42657d5fd66f5e250a1c3feb3ed08dcedad6416aa74c6",
		"0004_updates_stack_id.sql":                "a675c7933b232d32423a5ab8860dc640f689bb8469308d31a454060b61f4645e",
		"0005_updates_drop_names.sql":              "f5f114ec38c7f392e8351d433126d1b4c23c2ba9bdaf373b43be2167eca3bc0c",
		"0006_checkpoints_per_update.sql":          "445c2e2b56edb08e4081a9a05be076b298320569c785a24ed02ff85d7fb50191",
		"0007_stack_tags.sql":                      "a161443e20ce9a365d7e194dbfeacd52858ef618e15d93869d415ffd4430edd5",
		"0008_secret_keys.sql":                     "63dcead4d693ba7d70be0826f08890166e6b87d7dbd243f4ce81bcf08d65e095",
		"0009_inventory.sql":                       "00029ebba463aa004c2ed5cba8ff6a33c527cf6c668013a9082c70113feb0c42",
		"0010_workspaces.sql":                      "bd7c33726e3bcc5841401172eebaa45a658c378cb098f88c6ceeea64e04d6f39",
		"0011_restored_from.sql":                   "32a8f5ef75af5407cf56308fe9ba5198d564da7caedea353ad470f89c8e42f4a",
		"0012_update_history_fields.sql":           "04f3ff506ff836f27a3f9a3ee3528102b940fe3cd67b1a5c34eb5ea8e77dda9c",
		"0013_backups.sql":                         "cb17d80821eec5a4b98b833d58642dbbc7ec5f25bd57ac5b9bc24783674ee6be",
		"0014_checkpoint_resource_count.sql":       "c679dde7df1f391e322fa8ff5ba398b105bfa400a14331f220a871c32d3d9af2",
		"0015_maintenance.sql":                     "dd34cbf7f918f2c0282a9e4bbd5ffa7bcef4444ded393bf7268086c3527488d8",
		"0016_checkpoint_blobs.sql":                "ca9d54e8ad8d1569a41a071ce694098f24c35945055dc1b17b3dac4563a2df77",
		"0017_inventory_lock.sql":                  "c8a5cb4ba39f9dee84629ff5e004d70da5c4b2f84ab56e4f1daa40a373c197e2",
		"0018_restores.sql":                        "969dcd5e80a58822244a937de4aafeac485982dc40905566a5b79f0e3ea94aba",
		"0019_audit_entries.sql":                   "07fda9683ac6fe7d0479e1eb6a8dc16711a5eaf1afa42c2097afd337f04cba35",
		"0020_sealed_state.sql":                    "f7d97d88a2afcfa287ea6c2b20dda2f4263dfa0faea2ba286d4f03c304f6e5fe",
		"0021_sensitive_marks.sql":                 "cb509db8b4a8b99f8b06a635316914a0b234aed33fb12207fce246fd123dad3c",
		"0022_audit_value_count.sql":               "c07731db226c2fb9c5814d6319aebfb7f7b91853ad3d7b3d7b10aa6728cbc833",
		"0023_update_journal_version.sql":          "d48f5f9309335b60985b15329c37041ee6bd5ac75425b2db8dc4bdc3de21e3d8",
		"0024_state_fence.sql":                     "b6c73962a987448e7feeda2622535df7bc2dd78ad31cbdf3dbf2a47b24572ef7",
		"0025_update_journal.sql":                  "fa18c4c595a5b61e41d1d1c2c0b16aa28ea6807208faa74c49edb435f1bc7bb4",
		"0026_override_switches.sql":               "263bf5fbd9b08d9c71ad865aa356907a644cb4b7358fff105cf378b727044ee2",
		"0027_update_journal_md5.sql":              "02780d149c4bf03acbf27c0ca2a35f7e76d877b0abaafd00d817bb23978f3b84",
		"0028_principals_and_groups.sql":           "053b65373a9351044bfed7517fbf13447a60d6f90d5b489f5677d33f0af361d0",
		"0029_audit_server_scope.sql":              "50c4b54a75aeb3125ec73fc9427e439e30f058efbf08b2ca445b622734df9bd9",
		"0031_soft_delete_stacks.sql":              "94902a7b8b8f710eeed4ac22b9abef7a5bd9b856ba77d671d3efc1e246ea258b",
		"0032_soft_delete_workspaces.sql":          "0ad4fc7201fd387375327c0c05fffa7e927a1db377a5812f93edea3f102fcdac",
		"0035_access_tokens.sql":                   "aacb1bd2b0ea84d6b9c16960fccd2af09e8466afc54819dd3f34b545c49cd71e",
		"0042_roles_and_grants.sql":                "5e4cfab98773407ddb05a166ceb4287826bfdc8dc0330de6250ac751f2fd1b82",
		"0050_update_token_id.sql":                 "f162701f6dc2931b27a1fbd5e247b6b0962193b6557b52c2ffe7e04005f2ae9f",
		"0048_audit_changes.sql":                   "4a4b1cba2d407ebd986c54a405d025186ae6b9f5b867cc2a5333e8f54e825cec",
		"0049_audit_sink_cursors.sql":              "681d980748c2408d25a69857a4d5454ee38d9bc027834e8a6a70f63203841405",
		"0054_token_policy.sql":                    "66f78c48117299e60aeb4f4982cec7fb55ef828f8800206025b68962eee20a26",
		"0056_audit_object_id_and_blob_purges.sql": "e9e35129192bee53fe647a7123ac492e999e1d65026fe499a05c1854aaa7a612",
		"0057_namespace_ip_allowlist.sql":          "24567a6a4104e14e1909dfeb3489cc7fafebcded41fd1672980b3877006ba051",
		"0058_inventory_deleted_purge_after.sql":   "ae262446a4898c4b085aeadb0b62ad0f3187715a4dad77b6627553b8b4892ea5",
		"0059_collections.sql":                     "6d4e20c27e71dac697e49f05ae9174d2c050af076ddff837a21ddf57c713273b",
		"0062_sessions.sql":                        "7205e1eb6df9e51dfc56cb8694b8272fcb20a131f68edea2d353326484018d4f",
		"0064_blob_purges_age_byte_order.sql":      "ff017b110980e1ad8137fb3e64529f58da9c70f0f710b7838a40dd50cc5abfc1",
		"0066_audit_chain_state.sql":               "8b26321acdfe4306f7007ec292196d1cfb2247bdee14f7c06ac0dd0ee73cf7e5",
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
