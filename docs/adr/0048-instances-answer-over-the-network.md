# 48. Instances report maintenance mode over the network

Status: accepted · 2026-10-13

## Context

ADR 38 decision 4 keeps the installation state in the database. Each
instance caches it for a short time, so the restore apply step must know
that every instance sees maintenance mode before it replaces the live data.
ADR 38 leaves that wait open.
[RFC 60](../rfc/0060-maintenance-mode-visibility-wait.md) gives three
options: a fixed wait, asking every instance, or no cache.

On 2026-10-12 the project owner chose to ask the instances: "I think I want
instances to answer over the network." The owner chose a registry in the
database with an advertised address for discovery, and the public API with
an administrator action for the calls between instances. The owner asked how
SWIM gossip handles a dead peer. SWIM marks a peer that does not answer as
"suspect", then as "dead" after a timeout, but "dead" only means that the
peer cannot be reached. It does not mean that the peer stopped. So the
project lead proposed a lease rule. On 2026-10-13 the owner accepted the
lease rule and the rules that delete the row of a dead instance. The project lead then
explained that an instance keeps no data of its own and that the fence
refuses late writes. The owner answered: "So it's more of an issue when we
set up async geo replication. Got it."

## Decision

### The registry

1. **Each instance registers.** At start, an instance writes a row to an
   instance registry in the database. The row holds a new registration ID,
   the address from `--advertise-address`, the server version and a lease
   end time.
2. **An instance holds a lease.** An instance renews its lease every few
   seconds. A renewal succeeds only when the row still exists with the
   registration ID of the instance. When a renewal fails, or the lease ends
   before a renewal succeeds, the instance stops serving writes at once. It
   then registers again as a new instance with a new registration ID. It
   never continues an old registration.
3. **One address, one live instance.** An instance does not start when
   another row with the same advertised address holds a lease that has not
   ended. The error names that instance. When the lease of that row has
   ended, the new registration replaces the row and writes an audit entry.
4. **A server version check at start.** An instance that does not support
   the schema version of the database does not start. The error names the
   schema version. An instance never writes to a database that it cannot
   read correctly.

### The wait before a restore applies

5. **The apply step asks every instance.** After the server enters
   maintenance mode, the apply step calls each registered instance that
   holds a lease. Each instance answers with the installation state that it
   sees. The apply step continues only when every such instance answers
   `maintenance`.
6. **The calls use the public API.** The call is a route of the
   installation API with its own administrator action (ADR 17). The calling
   instance uses a credential that the policy allows for that action. There
   is no second API for the calls between instances.
7. **The apply step skips an instance only when its lease ended.** It
   skips an instance only after the lease end time plus a bound for clock
   skew. By that time the instance stopped serving writes (decision 2).
8. **A live instance that does not answer blocks the restore.** When an
   instance holds a lease and does not answer, the restore fails without
   changing anything, and the server leaves maintenance mode. The error
   names the instance. The apply step never skips an instance that holds a
   lease.

### Deleting the row of an instance

9. **A clean stop deletes the row.** An instance that stops normally deletes
   its own row as its last step.
10. **A sweep deletes old rows.** Each instance sweeps the registry from time
    to time. It deletes the rows whose lease ended more than a retention
    time ago. The retention time is a setting, and the default is 24 hours.
    Each delete writes an audit entry.
11. **An administrator can delete a row.** A route of the
    installation API deletes a row, with its own administrator action
    (ADR 17). The route refuses to delete a row that holds a lease, because
    that instance can still be serving.

### An instance that comes back

12. **Instances keep no data of their own.** All data is in the shared
    database and blob store. An instance holds only caches and the requests
    that it is serving. An instance that comes back, for example a virtual
    machine restored from a snapshot, registers as a new instance
    (decision 2), clears its caches and reads the database. It does not
    merge or replay anything.
13. **The fence refuses late writes.** A write in a namespace carries the
    fence of the namespace, and the store checks it in the transaction of
    the write. A restore moves each namespace to a new fence. So a write
    that a process started before a restore fails when it arrives after the
    restore. The client gets an error and can try again on a live instance.
    A blob that such a write stored before its row stays without a row, and
    the sweep for orphan blobs deletes it.

## Consequences

- The fixed wait of RFC 60 option 1 and its clock-skew question go away for
  the apply step. A clock-skew bound remains for the lease end (decision 7).
- A restore after a server restart asks the instances again. It does not
  need the enter time of the restore record.
- The server gets a registry table, a migration in both stores, two routes
  and two actions in the installation API, and tests for each decision. The
  tests include an instance that wakes up after a restore, and an instance
  that starts against a newer schema version.
- An installation with one instance registers one row and asks only itself.
- Asynchronous replication between regions is out of scope. There, two
  regions can each hold writes that the other has not seen. A future RFC
  must decide how they reconcile.
