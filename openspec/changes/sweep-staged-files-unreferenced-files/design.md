## Context

The first registry slice deletes staged files only during a later stage in the same namespace. A namespace that uploads once keeps its files. A refused publish leaves content blobs with no reference. The change adds one installation task. The age is fixed at 24 hours in code and the docs page states it.

## Decisions

1. **One durable scheduled sweep.** One schedule row of the installation claims the due tick on one instance and starts one durable run. The run pages namespaces, then staged keys by stored age, then content keys by reference. It records the last key for each page in a control table. Each delete checks the fencing token. A takeover resumes from the last key. A store error fails the step and the durable run retries with backoff.
2. **Mark, delete and re-copy ordering.** The mark table is a control table. Step 1: in one transaction the sweep checks zero live references and a blob age above 24 hours, then writes a mark with a database-clock expiry of 1 hour. Step 2: a publish that needs the blob reads marks in its own transaction. If a live mark exists, the publish waits for the delete to finish or the mark to expire, then copies the blob again. Step 3: the sweep re-checks the references and the mark in a transaction, deletes the blob from the store, then removes the mark. A publish writes its reference in a transaction that takes a row lock on the mark key, so the re-check and the reference write cannot both pass. An expired mark is ignored and removed by the next run.
3. **Unknown keys and the staged marker.** A key under the registry prefix that no row knows is deleted only when its store listing age is above 24 hours. A publish writes a moved flag on the staged row in its own transaction. The sweep re-reads that row in the delete transaction and keeps a moved file.
4. **Maintenance mode and restore.** The sweep makes no write in maintenance mode. A restore runs in maintenance mode, so the sweep cannot delete a blob that a restore wrote. The mark table is outside the restore swap, so a restore brings back no old mark. The metric has installation-wide counters with no namespace label.

## Structure

Unexported type registrySweeper with unexported fields clock, pageSize and ageLimit. Only newRegistrySweeper makes it. It rejects a page size below 1 and a negative age. An unbounded or clock-free sweep cannot exist. Marks key on the namespace ID and the content hash, not on a name.

## Risks

- A reference check that misses a table deletes a live blob. The conformance suite pins the reference set.
- A full listing of a large store stalls the run. A per-page limit bounds each transaction.
- Two instances run one tick. The claim and the fencing token keep one winner.
- A stale mark delays a publish. The 1-hour expiry bounds the delay.

## Layers (defense in depth)

| Layer | Guard | Hostile test |
| --- | --- | --- |
| port adapter | The blob delete and the key listing accept only keys of the registry prefix. | TestRegistrySweepRefusesForeignPrefix: a key outside the registry prefix is never deleted. |
| service | The delete step checks the fencing token, the reference count, the mark and the maintenance state in its own transaction. | TestRegistrySweepStaleTokenDeletesNothing: a run with an old token deletes no key. |
| publish path | A publish locks the mark row and refuses to reference a blob with a live mark. | TestRegistrySweepPublishRace: a publish and a delete run at the same time and the release stays readable. |
