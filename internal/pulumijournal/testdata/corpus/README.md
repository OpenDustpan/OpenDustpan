# Journal corpus

Each folder holds one recorded case. The corpus test replays the batches of
each case on the base deployment and compares the result with the recorded
export.

## Layout

- `base.json`: the deployment that the server served at the start of the
  update. The test reads it once for each replay pass.
- `batch-0001.json`, `batch-0002.json`, and so on: the accepted batch bodies
  in arrival order. The file order is the arrival order. Each file holds one
  body of the form `{"entries": [...]}` with the full entry objects.
- `export.json`: the `stack export` of the same program taken with
  journaling off, after the same update. The test normalizes it before the
  comparison.

The test replays each case twice: once with the entries in arrival order
(file order, body order within each file) and once with the same entries in
`sequenceID` order. Both results must match the recorded export after
normalization.

## Normalization

The comparison drops the fields that always differ between two runs of the
same update: the manifest, the metadata, the `state` of the secrets
provider, and the `created` and `modified` times of each resource. It
replaces each provider-assigned UUID with one fixed marker, and it sorts the
resources by URN, because the checkpoint lists them in engine completion
order and the replay lists them in journal order.

## How to record a new case

The e2e HTTP log truncates bodies, so it cannot supply the batch files.
Capture the full bodies instead, for example from the stored journal blobs
of a run with journaling on.

1. Start from a fixed program and a fixed CLI version. Note the version with
   the case.
2. Run the update with journaling on. Keep the deployment that the server
   served at the start of the update as `base.json`, verbatim.
3. Keep the body of each accepted batch as `batch-NNNN.json`, numbered in
   arrival order, verbatim. Keep each refused batch out of the corpus.
4. Run the same update with journaling off, from the same start state. Keep
   its `stack export` as `export.json`, verbatim.
5. Keep the case small: only the resources that the update changes. A case
   with secrets must use made-up values only.
6. Run `go test ./internal/pulumijournal/ -run TestCorpusEquivalence`. Both
   orders must pass. When they differ, examine the difference before
   changing the files: a difference means the replay builds other state
   than the checkpoint path.
