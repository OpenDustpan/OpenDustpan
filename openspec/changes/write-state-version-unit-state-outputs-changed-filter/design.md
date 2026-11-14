## Context

A follows trigger with when applied starts after each apply. Teams with many followers get noise when an apply changes no shared output. This part adds a digest compare before dispatch so only a changed root-outputs set starts follower plans.

## Decisions

1. **Digest at final write.** Store one opaque root-outputs digest with each final state version at final write, computed over canonical root outputs with secret values replaced by a fixed marker, never the values.
2. **Compare in Dispatcher.Upstream.** Dispatcher.Upstream loads the digest of the new final version and the digest of the immediate previous final version of the same source in the same read, and skips followers whose when is outputs-changed when the two digests match; a missing prior digest counts as changed.
3. **No digest leaves the server.** Digest is server state only: no API field, no log value, no notification value; the skipped outcome reports the reason outputs-unchanged with the source version pair and no value.

## Structure

Unexported type outputsDigest with constructor newOutputsDigest(canonicalRootOutputs) that rejects secret plain text; only the constructor builds the stored bytes and the store keeps the bytes beside the final version.

## Risks

- Digest canonicalization drifts between Pulumi and OpenTofu shapes and misses a change or reports one too many.
- Backfilled versions without a digest start one extra round of follower plans.
- Two applies at the same time serialize on the source unit write, so the compare reads committed digests in version order; a restart between final write and dispatch replays the dispatch from the stored final version.
