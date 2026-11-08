# Launcher

`dustpan-launcher` is the program that runs one claimed phase of a job. A
runner starts it in a sandbox or in a Kubernetes Job, and gives it a handoff
token. The launcher opens a job session on the server with that token, runs
each step of the phase, sends the log of each step to the server, and
finishes the phase.

The launcher is one static binary with no cgo. It runs steps on Linux only.
It also builds on macOS, where the mode `run` stops with the exit code 67. It
needs nothing from the image of the steps: no shell, no git and no C library.

Build it with `make build-launcher`. The binary is `bin/dustpan-launcher`.

## Modes

| Command line | What it does |
| --- | --- |
| `dustpan-launcher run --server <URL> --handoff <handoff>` | Runs one claimed phase. |
| `dustpan-launcher copy --dest <directory>` | Copies the binary to the directory as `dustpan-launcher` with mode 0755, and stops. The init container of a Kubernetes Job uses it. |
| `terraform-credentials-dustpan get <host>` | The credentials helper of OpenTofu. |
| `git-credential-dustpan get` | The git credential helper of the git credential mode `forge`. |

The two helpers are links to the launcher binary. The launcher selects the
helper from the name of the link. A helper asks the launcher of the step for
a token over the Unix socket that `DUSTPAN_HELPER_SOCKET` names. The helper
of OpenTofu writes `{"token": "..."}` for the host of the server and `{}` for
other hosts. The git helper writes a token only for `https`. The helpers
refuse `store` and `forget` of OpenTofu, and ignore `store` and `erase` of
git.

## Flags of the mode run

| Flag | Value |
| --- | --- |
| `--server` | The URL of the server, with `https`, and with no user, query or fragment. Use the public URL of the server: the session exchange signs it. |
| `--handoff` | How the launcher gets the handoff token: `pipe`, `stdin`, `kubernetes` or `vsock`. |
| `--work-dir` | The directory of the checkout, the home directory and the temporary directory of the steps. The default is `/dustpan/work`. |
| `--secrets-dir` | The memory-backed volume of the credential files. The default is `/dustpan/secrets`. It must be on `tmpfs` or `ramfs`. |
| `--coordinator`, `--ca-file`, `--token-file` | For the handoff `kubernetes` only. |

No flag and no environment variable carries a credential. The launcher
reads the proxy variables `HTTPS_PROXY` and `NO_PROXY` of its own
environment for its calls to the server. A step never gets them from the
launcher. An unknown flag, a
flag twice or a flag with no value stops the launcher with the exit code 64.

The handoff `pipe` reads the token from file descriptor 3. The handoff `stdin`
reads it from the standard input. Each one must be a pipe: the launcher
refuses a regular file, a terminal and a socket. The launcher reads the token
once, closes the pipe, and never writes the token to its output or to a log.

## What the mode run does

1. It sets `PR_SET_DUMPABLE` to 0, and `PR_SET_NO_NEW_PRIVS` to 1 on each
   thread. A process of a step cannot read the memory of the launcher, and a
   step cannot get more privileges through a set-user-ID program. It sets
   close-on-exec on each descriptor that it got from the runner, except the
   handoff pipe until it reads it. A step gets only its standard input,
   output and error.
2. It checks that the secrets directory is on `tmpfs` or `ramfs`, and creates
   in it a directory with mode 0700. It deletes that directory at the end.
3. It reads the handoff token and exchanges it at `POST /runner/v1/session`
   for a job session. The launcher makes the Ed25519 key of the session in
   memory, and signs each later call with it. The launcher sends its release
   in the header `Dustpan-Release`. When the server answers
   `version-ahead`, the launcher tries again every 10 seconds for at most
   15 minutes.
4. It renews the claim at a third of the lease, at
   `POST /runner/v1/session/renew`.
5. For each step, in order, it:
   1. records the start of the step.
   2. starts the argument list of the step with no shell, in its own process
      group, with the standard input on `/dev/null`, and the standard output
      and the standard error on one pipe.
   3. sends the output as log chunks of at most 256 KiB, at least every
      2 seconds, to `PUT /runner/v1/session/steps/{step}/logs/{seq}`, after
      the masks.
   4. ends the process group of the step, also the processes that the step
      left running in that group. A process that makes its own session or
      process group with `setsid` or `setpgid` leaves the group, and this
      release does not end it.
   5. records the end of the step with its exit code, the number of chunks
      and the SHA-256 of the log.
6. It finishes the phase at `POST /runner/v1/session/finish`. The phase
   succeeds when each step ends with the exit code 0, or with another code
   on a step that allows a failure. The first step that fails stops the
   phase, and the finish names the step.

### The program and the environment of a step

The first argument of a step is a clean absolute path, or a bare name that
the launcher finds in the absolute directories of its own `PATH`. The
launcher refuses a relative path and an argument with a NUL byte. The
directory of a step must be inside the checkout, also through a link.

A step never gets the environment of the launcher. It gets `PATH`, `HOME`,
`TMPDIR`, `LANG=C.UTF-8`, the variables of the launcher, and its own
variables. The variables of the launcher are `DUSTPAN_JOB_ID`,
`DUSTPAN_PHASE`, `DUSTPAN_STEP`, `DUSTPAN_UNIT`, `DUSTPAN_NAMESPACE` and
`DUSTPAN_SERVER_URL`. A step variable that sets `PATH`, `HOME`, `TMPDIR` or
`LANG`, that starts with `DUSTPAN_`, or that names a credential, fails the
step before its program starts.

### Masks

The launcher replaces each secret value that it holds with `[masked]` in the
log of each step, also when the value arrives in two reads or two chunks. A
value shorter than 4 bytes is not masked. In this release, the launcher masks
the handoff token.

### Timeouts, cancels and signals

At the timeout of a step, at a cancel of the job, and when the launcher gets
`SIGTERM` or `SIGINT`, the launcher sends `SIGINT` to the process group of the
step. After the grace period it sends `SIGKILL`. The grace period is
10 minutes for a step that changes resources and 10 seconds for any other
step. A step with no timeout has a timeout of 1 hour.

When the claim ends under the launcher, the launcher sends `SIGKILL` to the process group at
once, reports nothing more, and stops with the exit code 69.

## Exit codes

The runner reads the exit code to decide what to do with the claim.

| Code | Meaning | The runner |
| --- | --- | --- |
| 0 | The phase ended, and the server has its outcome, also a failure. | Deletes the Job or the sandbox. |
| 64 | The command line is not valid. | Releases the claim. |
| 65 | No valid handoff token, or the session exchange failed. No step started. | Releases the claim. |
| 66 | The server refused the release of the launcher. No step started. | Releases the claim. |
| 67 | The place is not safe: the secrets directory is not in memory, or the work directory is not an absolute path. No step started. | Releases the claim. |
| 68 | The source failed, and the launcher reported the failed phase. In this release, it means that the launcher cannot download a source (see below). | Lets the claim expire. |
| 69 | The claim ended under the launcher. | Deletes the Job or the sandbox. |
| 70 | The launcher could not report the end of the phase. | Lets the claim expire. |
| 71 | An error in the launcher. | Lets the claim expire. |

A helper mode stops with 0 when it answered, and with 1 when it failed.

## Not in this release

- **Source.** The launcher does not download a source snapshot. A phase with
  a source fails with the reason "this launcher release cannot download a
  source snapshot", and the launcher stops with the exit code 68. The
  contract of the launcher gives 68 to a source that failed its check or
  its checkout. This release uses it also for a source that it cannot
  download, because the runner does the same with the claim in both cases.
  Each step runs in an empty checkout directory.
- **Credentials.** The launcher does not ask for the credentials of a tool
  step, and does not serve the helper socket. A tool step whose assignment
  names a profile fails before its program starts, with the reason
  `this launcher release cannot deliver the credentials of profile "<name>"`,
  where `<name>` is the name of the profile. Its log holds the line
  `dustpan-launcher: this launcher release cannot deliver the credentials of
  profile "<name>"`. A tool step with no profile runs with no credential,
  and its log starts with the line `dustpan-launcher: this launcher release
  gives no credentials to a tool step`.
- **Plans.** The launcher does not upload the outputs of a step. A step
  with an output that a later phase of the job needs fails before its
  program starts, with the reason `this launcher release cannot upload
  output "<kind>" that a later phase needs`, where `<kind>` is the kind of
  the first such output. A step whose declared outputs no later phase reads
  runs, and its log holds the line `dustpan-launcher: this launcher release
  does not upload declared outputs`. A phase with a plan to apply fails with
  the reason "this launcher release cannot read the plan of an apply phase".
- **Sweeps.** The launcher does not run a sweep. It stops with the exit
  code 71.
- **Handoffs.** The handoffs `kubernetes` and `vsock` stop the launcher with
  the exit code 65.
- **Step users.** The steps run as the user of the launcher.
