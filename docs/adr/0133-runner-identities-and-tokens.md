# 133. Runner identities, join tokens, handoff tokens and job sessions

Status: accepted · 2026-10-18 · supersedes ADR 101 decision 10 in part · answers an open question of ADR 101

Changed by:

- ADR 204 extends: the open question on a token issuer, in part.

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
   checks and uses the token, and stores the runner with the key. A second
   join with one token fails with `token-used`.

   A join with a name that the pool has, of a runner that is not retired,
   is a **join again**. It keeps the runner ID and replaces its key, and
   only for a runner that is offline or has the status `must-rejoin`
   (`runner.Runner.CanRejoin`). A join again of a runner that still sends
   heartbeats fails with `runner.ErrRunnerOnline`, so a leaked join token
   cannot take the name of a working runner. The store decides in the
   transaction of the join whether it is a join again, and returns that
   (`runner.Joined`), so the server appends the audit entry
   `runner-rejoined` or `runner-joined` from what the store found
   (`runner.JoinEvent`, ADR 138). In the same transaction, a join again:
   - expires every live claim of the runner that has no job session, so a
     takeover gets those claims,
   - keeps every claim that has a job session. Its launcher holds its own
     session key, renews the claim through its session, and runs until it
     ends,
   - clears the previous key and the time of the last rotation, so no key
     of the runner from before the join works.
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
   refuses a time more than 60 seconds from its clock, and accepts a time
   at exactly 60 seconds (`runner.CheckProof`).

   On the four paths that are not safe to repeat (the rotation, the poll,
   the handoff and the session exchange), the server keeps the nonce of
   each proof in a nonce store until 2 minutes after the time of the
   proof (`runner.NonceExpiry`, `runner.SessionStore.UseNonce`). It refuses
   a second proof with the same nonce for the same subject with
   `bad-proof` (`runner.ErrReplay`), up to and at that expiry, so a replay
   at exactly the end of the proof window fails. Each instance checks the
   proof window on its own clock. ADR 72 puts durable claims on the clock
   of the database and sets no bound for the clocks of the instances, so
   this design sets one for proofs: the clocks of two instances differ by
   at most 60 seconds (`runner.MaxClockSkew`). The expiry is the 60
   seconds of the proof window plus that skew (`runner.NonceTTL`). The
   store deletes a nonce only after its expiry on the clock of the
   instance that deletes it. So no instance accepts a proof whose nonce
   another instance deleted. The
   session exchange carries a proof by the new session key, whose subject
   is the SHA-256 of the handoff token. On every other path, a proof that
   an attacker replays inside its window can only repeat the same request,
   with the same body, and that request is safe to repeat (ADR 131
   decision 13). TLS protects the requests, and the proof makes a leaked
   request useless after 60 seconds.
6. **Key rotation (owner).** A key older than 25 days gets `rotateKey` in
   the heartbeat answer. The runner then makes a new key pair and calls
   the rotation. The proof of the request uses the old key, and the body
   holds a signature of the new key (`runner.RotateChallenge`). The server
   replaces the key in one write, and keeps the old key as the previous
   key. For 5 minutes after the rotation (`runner.RotateGrace`), the server
   accepts a proof of the previous key for a retry of that rotation only:
   a rotation request whose new key is the current key. It then answers
   as for the first request and changes nothing. A proof of the previous
   key for any other new key fails with `bad-proof`, so a leaked old key
   cannot rotate the runner to a key of its own
   (`runner.Runner.CheckRotation`, `runner.Runner.RotationKeys`). The store
   checks this in the transaction of the rotation. Every other request
   needs the new key at once (`runner.Runner.ProofKeys`). So a runner that
   lost the answer of its rotation sends it again with the old key and the
   same new key, and gets the same answer. The runner keeps the old key until a request with
   the new key succeeds. A key older than 30 days authenticates nothing: the server answers
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
   The retire expires its claims in the same transaction, and the takeover
   rules apply. It also ends every job session of the runner: each later
   request of such a session fails with `retired`. Its record stays for
   the audit log.
9. **Handoff tokens (owner, for the deliveries).** For each claim, the
   runner asks the server for one handoff token, and gives it to the
   launcher. The handoff token:
   - is good for one exchange, for one claim of the runner that asks for
     it, while that claim is live. The server checks that the owner of the
     claim is the runner of the request, so a runner never gets a token
     for the claim of another runner (`claim-lost` or `not-found`),
   - lives 10 minutes (`runner.HandoffTokenTTL`),
   - starts with `dph_`, and the server keeps only its SHA-256,
   - never passes through an environment variable, a command-line
     argument or a file on a disk.

   The runner keeps no copy after the handoff. A second token for a claim
   with no session replaces the first: the first then opens nothing. So a
   pod that waited for a node longer than 10 minutes can get a new one. A
   new token never moves the claim time that the first token recorded, so
   no token opens a session later than 30 minutes after the claim time
   (ADR 131 decision 7). A token for a claim with a session fails with
   `conflict`.
10. **The deliveries of a handoff token (owner)** (`runner.HandoffFor`):
    - **Kubernetes: TokenReview pull.** The pod gets a projected service
      account token with the audience `opendustpan-coordinator` and the
      shortest lifetime, 10 minutes. The launcher sends it to the
      coordinator over TLS. The coordinator checks it with a TokenReview,
      reads the pod, and checks that the pod belongs to the Job of the
      claim (`runner.CheckHandoffPod`). Then it answers the handoff token,
      once (ADR 136).
    - **gVisor containers: standard input.** The machine runner writes
      the token to the standard input of the launcher in a gVisor
      container, and closes it (ADR 137).
    - **Firecracker microVMs: vsock.** The machine runner writes the token
      to a vsock connection to the launcher in the microVM, and closes it.
      The serial console never carries the token. If the microVM has a
      console, it is raw with no echo, and the coordinator never logs
      what it reads from it (ADR 137).
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

- A stolen runner key gives the attacker every credential of every job
  that the server routes to the pool of the runner, until an
  administrator retires the runner. With the key, the attacker claims
  work, asks for the handoff token of each claim, opens the job session
  and asks for the credentials of each tool step. The limit is one pool,
  in one namespace: the key claims no work of another pool, and no pool
  of another namespace. A team that keeps the jobs of production apart
  gives them a pool of their own. The audit log records every join,
  rotation and credential issue (ADR 138), and a retire ends the claims
  and the sessions of the key at once.
- A leaked handoff token is good for at most 10 minutes and one exchange.
  The launcher of the claim then fails its exchange, and the runner
  releases the claim. The audit log records the refused exchange.
- A runner that is off for more than 30 days needs a person. This is the
  cost of the rotation rule.
- The server keeps a nonce store for four paths. Its size is the number
  of those requests in the last 3 minutes, because a proof can be up to
  60 seconds in the future and each nonce stays until 2 minutes after the
  time of its proof.
- A join again does not stop a launcher that runs. To stop it, an
  administrator retires the runner or cancels the job.

## Open questions

- Should the server sign a short-lived workload identity token for each
  session, for a cloud role that trusts only one unit or one sweep target?
  ADR 164 asks the same for sweeps. It needs a token issuer in the server.
- Does the console send a reminder to the creator of a join token that
  expires unused?
- Does a Kubernetes runner keep its key in a Secret, or does it join again
  at each start with a token that a Secret holds? A key in a Secret lets a
  person with read access to Secrets act as the runner.
