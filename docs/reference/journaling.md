# Journaling

With journaling, the Pulumi CLI sends one small journal entry for each
resource operation. The server builds the checkpoint of the update from
the entries. Without journaling, the CLI saves checkpoints itself.
The release build does not offer journaling yet. It answers journal
version 0 to every update, so the CLI saves checkpoints.

## Setting

The setting has an effect only on a build that offers journaling, such as
a build made for recording. In that build, journaling is on. One setting
turns it off. The flag is `--disable-pulumi-journaling`. The environment
variable is `DUSTPAN_DISABLE_PULUMI_JOURNALING`. The file key is
`disable-pulumi-journaling`. The default is `false`, so journaling stays
on. Set it to `true`. Restart the server. The server then answers journal
version 0 to each new update. The Pulumi CLI then saves checkpoints. The
setting changes only new updates. An update that already runs keeps its
mode.

## When the server refuses a journal batch

The server refuses a batch that holds an entry it does not know. It
answers 400 and stores nothing from the batch. The update then fails.
Follow these steps in order.

1. Set `DUSTPAN_DISABLE_PULUMI_JOURNALING` to `true`, or set the flag.
   Restart the server. Each new update then saves checkpoints with the
   same CLI.
2. The failed update already ended. The CLI marked it as failed. Run
   `pulumi up` again. That update saves checkpoints. Run
   `pulumi refresh` first when the failed update left resources that
   exist but are not in the state.
3. An update that runs during the restart keeps journal mode. The CLI
   retries a transport error 4 times, 1 second, 2 seconds and 4 seconds
   apart. A restart that takes more than about 7 seconds can fail that
   update too. Run `pulumi up` again when it does.
4. Open an issue in this repository with the program and the CLI version,
   so the case can join the recorded corpus and the replay can get a fix.

## A loss the server cannot see

The server cannot see a last batch that never arrives. After its retries,
the CLI ignores the error of such a batch and still sends `complete` with
`succeeded`. The server holds no refusal for it. It sees no gap, because
the missing entries come after the highest stored entry. The update ends
as `succeeded`, and the checkpoint lacks those entries. No measurement
covers this case. Run `pulumi refresh` to fix the outputs.

## Bounds

Each bound below has a number and the measurement behind it. Each size
bound answers 413. The CLI then splits the batch and sends each half. A
batch with one entry that breaks the batch bound still fails the update.
The build time bound answers 503.

| Bound | Number | Measurement | Answer |
| --- | --- | --- | --- |
| Batch body | 8 MiB for one batch. A batch with one entry may hold up to 32 MiB, because the CLI cannot split one entry. | One benchmark run with 10,000 resources measured 4185585 bytes. | 413 |
| Journal entries in one update | 1118481 | 32 MiB holds 559240 smallest states of 60 bytes. A create or a destroy of each one needs 2 entries. One benchmark run measured the 60 bytes. | 413 |
| Added links in one update | 559240 | Each smallest state adds 1 link. A no-op update on a large base adds no links, so it passes. | 413 |
| New states in one update | 32 MiB | One benchmark run with 10,000 resources measured 2210000 new-state bytes. States that fit still fail when the built checkpoint itself is too large. | 413 |
| New states plus added links in one build | 64 MiB | One benchmark run measured a peak heap of 25826176 bytes. This bound is not a bound on heap. | 413 |
| Build time at complete | 5 minutes | One benchmark run with 10,000 resources measured 210444745 ns/op. The project has not yet checked whether the CLI waits past 20 minutes. | 503 |
