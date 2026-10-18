# 134. The launcher contract

Status: proposed · 2026-10-18 · adds to ADR 101 decisions 7, 10 and 13

## Context

[ADR 101](0101-two-executors-write-back-and-runners.md) decides that a
runner is shell-free: the launcher starts each step as an argument list,
with no shell. It decides that no credential passes through an environment
variable, that OpenTofu gets the job token through a credentials helper,
and that the Pulumi CLI gets it from a file of mode 0600 on a
memory-backed volume. ADR 133 makes the launcher the holder of a job
session.

The project owner decided on 17 October 2026 (owner):

- The launcher is a static binary. It runs steps as argument lists. The
  image of the team holds only the tools.
- It is a separate binary in this repository. It runs on Linux only, on
  any `GOARCH`, and the tests of a release build run on `amd64` and
  `arm64`.
- OpenTofu gets its credentials through a credentials helper, which is the
  launcher. The Pulumi CLI gets them from a file of mode 0600 on a
  memory-backed volume, for the tool step only.

The label "(owner)" names a decision of the owner. The other decisions are
proposals of this design. The rules are in `pkg/launcher`.

## Decision

1. **One static binary (owner).** `dustpan-launcher` is pure Go, with no
   cgo, built with `CGO_ENABLED=0` for `linux` and each `GOARCH` of the
   release. It links no SQL driver and no forge client. It needs nothing
   from the image of the team: no shell, no git, no C library. The release
   publishes it in an image of its own, `opendustpan/launcher:<release>`,
   and in the release archive of the machine runner.
2. **Modes.** The launcher has four modes (`launcher.Parse`):
   - `run`: run one claimed phase or scan.
   - `copy --dest <dir>`: copy the launcher binary to a directory and end.
     The init container of a Kubernetes Job uses it, so the image of the
     team needs no launcher.
   - The credentials helper of OpenTofu, when the base name of the program
     is `terraform-credentials-dustpan`.
   - The git credential helper of the mode `forge`, when the base name of
     the program is `git-credential-dustpan`.
3. **The command line of `run`.** `dustpan-launcher run --server <URL>
   --handoff kubernetes|stdin|pipe`, and for `kubernetes` also
   `--coordinator <URL> --ca-file <file> --token-file <file>`. The flags
   `--work-dir` and `--secrets-dir` default to `/dustpan/work` and
   `/dustpan/secrets`. The server and the coordinator are `https` URLs with
   no user, query or fragment. A flag that is unknown, twice or without a
   value ends the launcher with the exit code 64. No flag carries a
   credential.
4. **The order of a run.**
   1. Check the place: the secrets directory is on `tmpfs` or `ramfs`, has
      mode 0700 and belongs to the user of the launcher
      (`launcher.MemoryVolume`). Else end with exit code 67.
   2. Get the handoff token through the handoff, and open the job session
      (ADR 133). A failure ends with exit code 65, or 66 for a release that
      the server refuses.
   3. Get the source snapshot, check it, and write the checkout (ADR 135).
      A failure reports the phase failed and ends with exit code 68.
   4. Run each step in order (decisions 5 to 9).
   5. Upload the plan, if the phase has one, then finish the phase.
5. **A step is an argument list (owner).** The launcher starts `Args[0]`
   with `Args[1:]`, with no shell. `Args[0]` is a bare name that the
   launcher finds on the `PATH` of the image, or a clean absolute path.
   The working directory is the directory of the step in the checkout. The
   standard input is `/dev/null`. The standard output and the standard
   error go to one pipe, which the launcher masks and sends as the log of
   the step. Each step runs in its own process group, with
   `PR_SET_NO_NEW_PRIVS`.
6. **The environment of a step.** The launcher never passes its own
   environment to a step. A step gets exactly (`launcher.BuildEnv`):
   - `PATH` of the image, `HOME` and `TMPDIR` in the work directory, and
     `LANG=C.UTF-8`,
   - the variables of the launcher, which start with `DUSTPAN_`: the job
     ID, the phase, the step, the unit, the namespace, the commit, the
     server URL, and for a scan the path of the report,
   - the variables of the step from the spec,
   - for a step of the role `tool` only: the variables of the tool and of
     the credential files. Each holds a path or a URL, never a credential.

   A step variable that sets `PATH`, `HOME`, `TMPDIR` or `LANG`, that
   starts with `DUSTPAN_`, that the credential-variable rule matches, or
   that a variable of the launcher also sets, fails the step. The server
   already refuses most of them at start (ADR 101 decision 7). The
   launcher checks again, because an old launcher can meet a new server.
7. **Credentials go to the tool step only (owner).** Before a step of the
   role `tool`, the launcher asks for the credentials of the step through
   its session (ADR 133 decision 12). It writes them under a directory of
   the step on the memory-backed volume, with mode 0600 for files and 0700
   for directories, and deletes that directory when the step ends. A step
   of the role `hook` gets nothing.
   - **OpenTofu (owner).** The launcher writes a CLI configuration that
     names the credentials helper `dustpan` and nothing else
     (`launcher.OpenTofuConfig`), and links itself as
     `terraform-credentials-dustpan` in the plugin directory of the home of
     the step. OpenTofu starts the helper with `get <host>`. The helper
     asks the launcher over a Unix socket in the directory of the step. The
     launcher answers the job token for the host of the server only, and
     `{}` for any other host (`launcher.OpenTofuAnswer`). The token stays
     in the memory of the launcher. The variable `TF_CLI_CONFIG_FILE` names
     the configuration.
   - **Pulumi (owner).** The launcher writes the credentials file of a
     Pulumi home directory, with the server URL and the job token
     (`launcher.PulumiCredentials`). `PULUMI_HOME` names the directory, and
     `PULUMI_BACKEND_URL` the server.
   - **Profile files.** The launcher writes each credential file of the
     profile, for example a shared credentials file of a cloud. The
     profile names the variable that points to the file, for example
     `AWS_SHARED_CREDENTIALS_FILE`. The variable holds the path.
   - **Git.** The launcher writes a git configuration that clears the
     credential helpers of the image, and names the helper of the git mode
     of the pool, if any (`launcher.GitConfig`). `GIT_CONFIG_GLOBAL` names
     it. The helper of the mode `forge` is the launcher itself, over the
     same socket.
8. **Masks.** The launcher masks each secret value that it got through its
   session in the log of every step, with `[masked]`, also across the end
   of a chunk (`launcher.Masker`). A value shorter than 4 bytes is not
   masked, because it would hide common text.
9. **Timeouts and signals (owner).** At the timeout of a step or at a
   cancel, the launcher sends `SIGINT` to the process group of the step.
   After the grace period, 10 minutes for a step that changes resources and
   10 seconds for any other step, it sends `SIGKILL` to the group. As the
   first process of a container, the launcher gets `SIGTERM` when
   Kubernetes or the runtime stops it. It then stops the step in the same
   way. The limits of the launcher (`runner.LimitsFor`) are the sum of the
   timeouts and the grace periods of the steps, plus 2 minutes to report.
   The runner sets them as the deadline of the Job or the sandbox.
10. **Exit codes.** The launcher ends with one of these codes
    (`launcher.ExitCode`). The runner reads the code to decide what to do
    with the claim (`launcher.DispositionFor`):

    | Code | Meaning | The runner |
    | --- | --- | --- |
    | 0 | The phase or scan ended, and the server has its outcome, also a failure. | deletes the Job or sandbox |
    | 64 | The command line is not valid. | releases the claim |
    | 65 | No handoff token, or the exchange failed. No step started. | releases the claim |
    | 66 | The server refused the release of the launcher. | releases the claim |
    | 67 | The place is not safe: the secrets directory is not in memory. | releases the claim |
    | 68 | The source failed its check. The launcher reported it. | lets the claim expire |
    | 69 | The claim ended under the launcher. | deletes the Job or sandbox |
    | 70 | The launcher could not report the end of the phase. | lets the claim expire |
    | 71 | An error in the launcher. | lets the claim expire |

    A code that this table does not have, or an end on a signal, lets the
    claim expire. The server then applies the rule of a lost claim, which
    fails the phase when a step that changes resources started (ADR 100
    decision 9). The codes stay below 126, so they never clash with the
    codes of a shell or of a signal.

## Consequences

- The image of the team needs no shell, no git and no OpenDustpan file. A
  distroless image with the tools works.
- A provider that reads a credential only from a variable does not work.
  This is the cost of ADR 101 decision 10.
- The launcher is the only code of OpenDustpan in the place of the step.
  Its size and its dependencies are part of the attack surface of each
  job. A test of the launcher package will fail when the binary links a
  SQL driver, a forge client or a module outside the standard library.
- A step can still read the credential files while it runs, as any tool
  can. The memory-backed volume keeps them off disks, and the directory of
  a step goes away when the step ends.

## Open questions

- Does OpenTofu find the helper in the plugin directory of the home of the
  step, or does the configuration need a `plugin_cache_dir` or another
  path? The e2e step of the launcher decides it with a real OpenTofu.
- Does the launcher write a minimal `.git` directory for tools that read
  git metadata, for example the Pulumi CLI for the update metadata? This
  design writes none (ADR 135).
- Does the launcher limit the CPU and the memory of a step, for example
  with a cgroup, or does it leave that to the Job and the sandbox?
