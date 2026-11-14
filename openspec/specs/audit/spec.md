# audit Specification

## Purpose
The audit capability covers the audit log: the entries that the server writes for each namespace and for the installation, and the hash chain that lets a person check them. Each entry names the actor, the action, the object by its stable ID and what changed, and it commits in the same transaction as the change. Sending the entries to an outside receiver is in the log sinks capability, and the settings for retention are in their own capability.

## Requirements

### Requirement: A stack rename writes an audit entry
The server SHALL append one audit entry in the transaction of a Pulumi stack
rename. The entry SHALL name the stable stack ID as its object ID and the action
`unit:manage`. It SHALL record one change with the key `name`, the old stack
name and the new stack name. The rename and the entry SHALL commit together or
not at all.

#### Scenario: Rename a stack
- **GIVEN** a live stack `acme/web/prod`
- **WHEN** a client renames it to `acme/web/live`
- **THEN** the audit log of `acme` holds one entry with the stack ID as `objectID`, the object `pulumi:acme/web/prod`, and the change `name` from `prod` to `live`
