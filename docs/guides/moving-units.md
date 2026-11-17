# Move a stack or a workspace to another namespace

A stack or a workspace stays in its namespace. The server has no action that moves it to another namespace.

To move a stack or a workspace to another namespace, export it from the source namespace and import it into the target namespace.

## Before you move

1. List the units in the source namespace and find the unit that you want to move.

   ```sh
   curl -sS -H "Authorization: Bearer $TOKEN" \
     "https://dustpan.example.com/api/dustpan/v1/namespaces/acme/units"
   ```

2. Export the unit from the source namespace. Use the export that your tool supports. For a Pulumi stack, see [Migrate out of OpenDustpan](migrate-out.md). For an OpenTofu workspace, pull its state or replay its state versions as [Import state into OpenDustpan](import.md) describes for a source.
3. Choose a target name that is free in the target namespace. A deleted unit holds its name until its retention period ends.

## Move the unit

1. List the deleted units of the target namespace when you are not sure that the target name is free.

   ```sh
   curl -sS -H "Authorization: Bearer $TOKEN" \
     "https://dustpan.example.com/api/dustpan/v1/namespaces/target/deleted-units"
   ```

2. Run a dry run of the import into the target namespace and read the plan.
3. Import the unit into the target namespace under the free name.

## What does not carry over

Export and import move the state, and the history where the tool exports it. They do not move these items:

- **Grants.** Grants on the unit, on its collection and on the source namespace stay with the source. Make the grants that the unit needs in the target namespace.
- **Sharing rules.** Output sharing rules do not move to the target namespace. Make the sharing rules that the unit needs in the target namespace.
- **Dependents.** References from other stacks or workspaces to this unit do not change. Update each dependent after the move.
- **The import hold.** The import hold is a manual lock on each target unit during the import. It does not carry over to the unit after the import ends.

## Errors

| Error | What the answer says | The command that helps |
| --- | --- | --- |
| The target name already holds a unit, deleted units included | The dry run marks that import unit skipped. Deleted units hold the name too. List deleted units, then pick a free target name. | `GET /api/dustpan/v1/namespaces/{namespace}/deleted-units` |
| The source unit is not found in its namespace | The server answers 404. Check the source name in the unit list of the source namespace. | `GET /api/dustpan/v1/namespaces/{namespace}/units` |

## After the move

Check the imported unit in the target namespace. Keep the source unit until the target unit works. Then delete the source unit when you no longer need it. A delete is a soft delete. See [Deleted units](deleted-units.md).
