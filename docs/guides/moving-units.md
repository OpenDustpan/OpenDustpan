# Move a stack or a workspace to another namespace

A stack or a workspace stays in its namespace. The server has no action that moves it to another namespace.

To move a stack or a workspace to another namespace, export it from the source namespace and import it into the target namespace.

## Before you move

Use one access token for the source namespace and one for the target namespace. Both namespaces are on the same server. A personal token and a service token both work. See [Access tokens](access-tokens.md) for the two kinds. The token needs read access to the source unit and write access in the target namespace.

1. List the units in the source namespace and find the unit that you want to move.

   ```sh
   curl -sS -H "Authorization: Bearer $TOKEN" \
     "https://dustpan.example.com/api/dustpan/v1/namespaces/acme/units"
   ```

2. Choose a target name that no live stack or workspace holds in the target namespace. A deleted stack or workspace does not hold its name, so you can use that name. The deleted one stays restorable until its retention period ends. A restore of it under its old name then answers 409 `unit.name-taken`, and you must send a new name. See [Deleted units](deleted-units.md). List the stacks and workspaces of the target namespace when you are not sure that the name is free.

   ```sh
   curl -sS -H "Authorization: Bearer $TOKEN" \
     "https://dustpan.example.com/api/dustpan/v1/namespaces/target/units"
   ```

3. Stop each pipeline that writes to the source unit. The import does not lock the source.

## Move a Pulumi stack

The export and the import use the Pulumi CLI. The steps use two saved logins on the same server, one for each namespace. The credential for each login is a Pulumi access token of the namespace. [Import state into OpenDustpan](import.md) has the full description of each step.

1. Run the dry run. It reads the source stack and changes nothing. Save the export and the history.

   ```sh
   export PULUMI_ACCESS_TOKEN=<source-token>
   pulumi login https://dustpan.example.com
   pulumi stack select acme/<project>/<stack>
   pulumi stack history --json
   pulumi stack export
   ```

2. Export each version that you want to keep, oldest first. Hold each export in a shell variable. The variable keeps the plain text in memory, with no file. Export one version to move the latest state only.

   ```sh
   EXPORT1=$(pulumi stack export --version 1)
   EXPORT2=$(pulumi stack export --version 2)
   ```

3. Log in with the token of the target namespace. Make the stack under the free name. Set the secret config values again. The target stack uses its own secrets key.

   ```sh
   export PULUMI_ACCESS_TOKEN=<target-token>
   pulumi login https://dustpan.example.com
   pulumi stack init target/<project>/<stack>
   pulumi stack select target/<project>/<stack>
   printf '%s' '<value>' | pulumi config set --secret <key>
   ```

4. Import each held export on standard input, oldest first.

   ```sh
   printf '%s' "$EXPORT1" | pulumi stack import
   printf '%s' "$EXPORT2" | pulumi stack import
   ```

5. Check the target stack. The report of the update holds the word `unchanged`.

   ```sh
   pulumi stack output --show-secrets
   pulumi stack history --json
   pulumi up --yes --non-interactive
   ```

To leave OpenDustpan instead of moving inside it, see [Migrate out of OpenDustpan](migrate-out.md).

## Move an OpenTofu workspace

The import uses the replay script `scripts/import-tfe-state.sh`. The credential for each side is a token of that namespace for the TFE-compatible API. The script reads the tokens from the environment and never prints them. It replays the newest `--count` state versions from the oldest to the newest. Each replayed version keeps its serial, lineage and MD5.

1. Run the dry run. It lists what the replay would move and changes nothing. It ends with `dry run, changed nothing`.

   ```sh
   export IMPORT_TFE_SOURCE_TOKEN=<source-token>
   export IMPORT_TFE_DEST_TOKEN=<target-token>
   scripts/import-tfe-state.sh \
     --source-url https://dustpan.example.com \
     --dest-url https://dustpan.example.com \
     --source-org acme \
     --source-workspace <workspace> \
     --dest-org target \
     --dest-workspace <workspace> \
     --count 10 \
     --dry-run
   ```

2. Run the same command without `--dry-run`. The script prints one line for each version. It ends with the count that it replayed and the count that it skipped. The script skips each version that the target already holds.

   ```sh
   scripts/import-tfe-state.sh \
     --source-url https://dustpan.example.com \
     --dest-url https://dustpan.example.com \
     --source-org acme \
     --source-workspace <workspace> \
     --dest-org target \
     --dest-workspace <workspace> \
     --count 10
   ```

3. Point the project at the target workspace and run a normal apply. It must succeed on the replayed state. Use the `cloud` block and the token file that [Import state into OpenDustpan](import.md) describes.

   ```sh
   tofu init -input=false -no-color
   tofu apply -input=false -auto-approve -no-color
   tofu state pull -no-color
   ```

## What does not carry over

Export and import move the state, and the history where the tool exports it. They do not move these items:

- **Grants.** Grants on the unit, on its collection and on the source namespace stay with the source. Make the grants that the unit needs in the target namespace.
- **Sharing rules.** Output sharing rules do not move to the target namespace. Make the sharing rules that the unit needs in the target namespace.
- **Dependents.** References from other stacks or workspaces to this unit do not change. Update each dependent after the move.
- **The import hold.** This version has no import hold. The import does not lock the target stack or workspace, and the imports route answers 404. Stop each writer to the target until the import ends.

## Errors

| Error | What the answer says | The command that helps |
| --- | --- | --- |
| A live stack or workspace in the target namespace already holds the target name | The Pulumi stack init fails with 409 `stack.already-exists`, or the replay script skips each version that the target holds. A deleted stack or workspace does not hold the name. List them, then pick a free target name. | `GET /api/dustpan/v1/namespaces/{namespace}/units` |
| The source unit is not found in its namespace | The server answers 404. Check the source name in the unit list of the source namespace. | `GET /api/dustpan/v1/namespaces/{namespace}/units` |

## After the move

Check the imported unit in the target namespace. Keep the source unit until the target unit works. Then delete the source unit when you no longer need it. A delete is a soft delete. See [Deleted units](deleted-units.md).
