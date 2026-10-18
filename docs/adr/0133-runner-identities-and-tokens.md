# 133. Runner identities, join tokens, handoff tokens and job sessions

Status: proposed · 2026-10-18 · supersedes ADR 101 decision 10 in part · answers an open question of ADR 101

## Context

[ADR 101](0101-two-executors-write-back-and-runners.md) asks whether a
runner is a service principal of its namespace or has a credential kind of
its own. Decision 10 of ADR 101 says that no credential passes through an
environment variable, and that the runner's own binary is the credentials
helper of OpenTofu. [ADR 100](0100-a-shared-job-model-on-durable-operations.md)
decision 20 defines the **job token**: the access token that a tool uses
for the state of the unit of a job.

The project owner decided on 17 October 2026 (owner):

- Join tokens are good for one use. They live 1 hour by default and 7 days
  at most.
- Runner keys rotate every 30 days.
- The credential that a coordinator gives to the launcher never passes
  through an environment variable. On Kubernetes the pod pulls it: the pod
  calls the coordinator with its projected service account token, and the
  coordinator checks it with a TokenReview. A container gets it on its
  standard input. A sandboxed process reads it once from an inherited pipe.
- The launcher exchanges it for a job session, bound to a key that the
  launcher makes (proof of possession).
- The docs and the console make the join of an idle runner clear, with the
  exact command.

The owner's text calls the credential that the coordinator gives to the
launcher "the job token". ADR 100 already uses "job token" for the access
token of a tool. This ADR calls the credential of the coordinator the
**handoff token**, so that one word names one thing. The job token keeps
the meaning of ADR 100.

## Decision

1. **A runner is not a principal.** A runner has its own credential kind: a
   runner key in one pool. It holds only `runner:claim` on its pool, and
   only through the protocol. It never calls the REST API. A service
   principal could get other grants by mistake. A runner key cannot.
2. **Join tokens (owner).** A principal with `namespace:manage-runners`
   creates a join token for one pool. The token is good for one join. It
   lives 1 hour by default, and at most 7 days (`runner.JoinTTL`). The
   secret starts with `dpj_`, so secret scanners find it. The server shows
   the secret once, and keeps only its SHA-256. A principal can revoke an
   unused token. A namespace has at most 100 open tokens for each pool.
3. **The join.** The runner makes an Ed25519 key pair. It signs a
   challenge with the new key: the server URL, the pool, the runner name
   and the hash of the join token (`runner.JoinChallenge`). It sends the
   token, the public key and the signature. In one transaction, the server
   checks and uses the token, and stores the runner with the key. A join
   with a name that the pool has, of a runner that is not retired, keeps
   the runner ID and replaces its key: this is a **join again**. A second
   join with one token fails with `token-used`.
4. **Where the runner keeps its key.** The machine runner keeps its private
   key in a file of mode 0600 that only its user reads. The Kubernetes
   runner keeps it in a Kubernetes Secret of its own namespace. The
   private key never leaves the runner.
5. **Proof of possession on each request.** Each request of a runner or a
   launcher carries the header `Dustpan-Proof`: the runner ID or the
   session ID, the time in Unix milliseconds, a random nonce of at least
   128 bits, and an Ed25519 signature. The signature covers a fixed
   context, the subject, the method, the path with its query, the SHA-256
   of the body, the time and the nonce (`runner.ProofInput`). The server
   refuses a time more than 60 seconds from its clock
   (`runner.CheckProof`). The server keeps no list of nonces. A proof that
   an attacker replays inside its window can only repeat the same request,
   with the same body. Every request of the protocol is safe to repeat
   (ADR 131 decision 13). TLS protects the requests, and the proof makes a
   leaked request useless after 60 seconds.
6. **Key rotation (owner).** A key older than 25 days gets `rotateKey` in
   the heartbeat answer. The runner then makes a new key pair and calls
   the rotation. The proof of the request uses the old key, and the body
   holds a signature of the new key (`runner.RotateChallenge`). The server
   replaces the key in one write. The runner keeps the old key until a
   request with the new key succeeds, so a lost answer does not lock it
   out. A key older than 30 days authenticates nothing: the server answers
   `key-expired`, and the runner must join again (`runner.MaxKeyAge`).
7. **A join again of an idle runner (owner).** A runner that was offline
   for more than 30 days has an expired key. Its status is `must-rejoin`.
   The runner log, the console and the guide show the same steps:
   - Create a join token for the pool, in the console or with the API.
   - Give it to the runner on its standard input. The machine runner
     command is `dustpan-runner join --server <URL> --pool <pool ID>
     --name <name> --token-stdin` (`runner.RejoinArgs`).
   - For the Kubernetes runner, put the token in the Secret of the runner
     and restart it. The console shows the `kubectl` commands with the
     namespace and the names filled in.

   The console page of the pool shows the command for each runner with
   the status `must-rejoin`, with the URL, the pool and the name filled
   in. It never puts the token in a command line or a variable.
8. **Retire a runner.** A principal with `namespace:manage-runners` can
   drain a runner or retire it. A retired runner authenticates nothing.
   Its claims expire, and the takeover rules apply. Its record stays for
   the audit log.
9. **Handoff tokens (owner, for the deliveries).** For each claim, the
   runner asks the server for one handoff token, and gives it to the
   launcher. The handoff token:
   - is good for one exchange, for one claim, while that claim is live,
   - lives 10 minutes (`runner.HandoffTokenTTL`),
   - starts with `dph_`, and the server keeps only its SHA-256,
   - never passes through an environment variable, a command-line
     argument or a file on a disk.

   The runner keeps no copy after the handoff. A second token for a claim
   with no session replaces the first, so a pod that waited for a node
   longer than 10 minutes can get a new one.
10. **The deliveries of a handoff token (owner)** (`runner.HandoffFor`):
    - **Kubernetes: TokenReview pull.** The pod gets a projected service
      account token with the audience `opendustpan-coordinator` and the
      shortest lifetime, 10 minutes. The launcher sends it to the
      coordinator over TLS. The coordinator checks it with a TokenReview,
      reads the pod, and checks that the pod belongs to the Job of the
      claim (`runner.CheckHandoffPod`). Then it answers the handoff token,
      once (ADR 136).
    - **Containers and microVMs: standard input.** The machine runner
      writes the token to the standard input of the launcher in a gVisor
      container, or to the console of the launcher in a Firecracker
      microVM, and closes it (ADR 137).
    - **Sandboxed process: an inherited pipe.** The machine runner writes
      the token to a pipe that the launcher inherits as file descriptor 3.
      The launcher reads it once and closes it.
11. **Job sessions (owner).** The launcher makes an Ed25519 key pair in
    memory. It never writes the private key to a disk. It sends the handoff
    token, the public key and a signature of the new key. In one
    transaction, the server checks and uses the handoff token, checks that
    the claim is live, and stores the **job session**. The session lives as
    long as the claim. Each later request of the launcher carries a proof
    of the session key. A session of a claim that a takeover replaced
    authenticates nothing (`claim-lost`).
12. **The job token goes to the tool, step by step.** The launcher asks
    for the credentials of each step of the role `tool` through its
    session. The server answers the job token of ADR 100 decision 20, and
    the credential files of the profile. A job token authenticates only
    while its claim is live, and at most until the end of the timeout and
    the grace period of its step. So a job token that leaks from a tool
    authenticates nothing after the phase. The launcher gives the token to
    the tool through the credentials helper of OpenTofu or the credentials
    file of the Pulumi CLI (ADR 134). A hook step never gets it.
13. **This supersedes ADR 101 decision 10 in part.** Decision 10 names the
    runner's own binary as the credentials helper, and the runner as the
    writer of the credential files. The launcher does both: it runs in the
    place of the step, and the runner never holds a credential of a step.
    The other rules of decision 10 stay.

## Consequences

- A stolen runner key claims work of one pool and reads no credential: the
  credentials go to sessions, which need a handoff token that the runner
  gets for one claim at a time. A stolen key still lets an attacker take
  claims and see the steps and the source of jobs. The audit log records
  every join, rotation and credential issue (ADR 138).
- A leaked handoff token is good for at most 10 minutes and one exchange.
  The launcher of the claim then fails its exchange, and the runner
  releases the claim. The audit log records the refused exchange.
- A runner that is off for more than 30 days needs a person. This is the
  cost of the rotation rule.
- The server needs no list of nonces, because every request is safe to
  repeat.

## Open questions

- Should the server sign a short-lived workload identity token for each
  session, for a cloud role that trusts only one unit or one scan target?
  ADR 164 asks the same for scans. It needs a token issuer in the server.
- Does the console send a reminder to the creator of a join token that
  expires unused?
- Does a Kubernetes runner keep its key in a Secret, or does it join again
  at each start with a token that a Secret holds? A key in a Secret lets a
  person with read access to Secrets act as the runner.
