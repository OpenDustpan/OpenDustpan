# 137. The machine coordinator

Status: accepted · 2026-10-18

Changed by:

- ADR 139 extends decision 4: go-containerregistry to get and unpack images.
- ADR 330 extends decision 6: one route from a sandbox, to the job proxy.

## Context

Some teams run no Kubernetes, or want a kernel boundary around each phase.
ADR 130 makes a runner a coordinator of the kind `machine` for them.

The project owner decided on 17 October 2026 (owner):

- On machines, a static coordinator starts each phase in a Firecracker
  microVM, which needs `/dev/kvm`, in a gVisor container, or in a
  sandboxed process. The sandboxed process is weaker and is for tests.
- A cloud VM for each job is shelved.
- A container gets its handoff credential on its standard input, and a
  sandboxed process reads it once from an inherited pipe.

The project owner approved every decision of this ADR. The label "(owner)"
names a decision that the text of the owner of 17 October 2026 states.
The port is `runner.Sandbox` in `pkg/runner`.

## Decision

1. **A static coordinator on its own host (owner).** The machine runner is
   a long-lived process on a Linux host that the team owns: a VM or a bare
   metal server. It joins one pool with one isolation kind, and starts one
   new sandbox for each claim. It destroys the sandbox when the launcher
   ends (`runner.SandboxHandle.Destroy`).
2. **Three sandboxes (owner)** (`runner.Isolation`):
   - `firecracker`: a Firecracker microVM, with its own kernel. The host
     needs `/dev/kvm`. This is the strongest boundary of the three.
   - `gvisor`: a container in the gVisor runtime (`runsc`), whose kernel
     runs in user space. The host needs no virtualization.
   - `process`: a child process in its own Linux namespaces (user, mount,
     PID, network as the pool sets it, IPC and UTS), with a seccomp
     filter. It has no kernel boundary. It is for tests and for a
     developer machine. A pool must list it by name (ADR 130 decision 2).
3. **A check before each claim.** Before it joins, and before each poll,
   the coordinator checks that the host can start its sandbox
   (`runner.Sandbox.Check`): for example that `/dev/kvm` exists and that
   the user of the coordinator can open it, or that `runsc` runs. A host
   that fails the check does not poll, and its heartbeat says why.
4. **The image of the team.** Each sandbox runs the image of the unit or of
   the pool, with the tools, as on Kubernetes:
   - For `gvisor`, the coordinator unpacks the image into an OCI bundle and
     starts it with `runsc`. It adds the launcher binary and a memory-backed
     volume for the secrets directory.
   - For `firecracker`, the coordinator makes a root file system from the
     image, and starts a microVM with the kernel of the release of the
     coordinator. The launcher is the first process of the microVM.
   - For `process`, the coordinator unpacks the image into a directory and
     starts the launcher with that directory as its root.

   The coordinator starts each external program, for example `runsc` or
   `firecracker`, as an argument list, with no shell.

   The project owner decides how the coordinator gets and unpacks an OCI
   image: with its own code, with a library, or with a container runtime
   of the host such as `containerd`. This design parks the choice for the
   owner and sets no default. The code of the machine coordinator does not land before the
   owner decides.
5. **The handoff (owner)** (`runner.HandoffFor`):
   - `gvisor`: the coordinator writes the handoff token to the standard
     input of the launcher and closes it.
   - `firecracker`: the coordinator writes the handoff token to a vsock
     connection to the launcher in the microVM, and closes it
     (`runner.HandoffVsock`). The serial console never carries the token.
     If the microVM has a serial console at all, it is raw, with no echo,
     and the coordinator never logs the input of the console.
   - `process`: the coordinator writes the handoff token to a pipe that
     the launcher inherits as file descriptor 3. The launcher reads it
     once and closes it.

   The coordinator writes the token once and keeps no copy.
6. **Network.** Each sandbox gets its own network namespace. The pool can
   set the egress of the sandbox: only the server and the hosts that the
   tools need, through the egress proxy of RFC 16 when the team runs it.
   The default is the network of the host, through a NAT of the
   coordinator.
7. **Limits of a sandbox.** The coordinator gives each sandbox a CPU and a
   memory limit (`runner.SandboxSpec`), from the settings of the
   coordinator. It ends a sandbox at its deadline: `SIGINT` to the
   launcher, then `SIGKILL` after the grace period (`runner.LimitsFor`).
8. **No VM for each job (owner).** The coordinator never creates a cloud VM
   for a phase. A team that wants one VM for each phase runs Firecracker
   on a host. A cloud VM for each phase is shelved.
9. **The end of a sandbox.** The coordinator reads the exit code of the
   launcher, and does what `launcher.DispositionFor` says. It then
   destroys the sandbox: its processes, its files and its memory-backed
   volume.

## Consequences

- A team with one Linux VM and `/dev/kvm` gets a kernel boundary for each
  phase with no Kubernetes.
- Most cloud VMs do not give `/dev/kvm` without nested virtualization.
  Such a team uses `gvisor`. The console shows the isolation of each
  runner.
- The `process` sandbox lets a developer run the whole path on a laptop,
  with a warning in the console. A production pool does not list it.
- The coordinator needs code to unpack an OCI image and, for Firecracker,
  to make a root file system. That code is part of the attack surface of
  the host, not of the server.

## Open questions

- Which kernel does a Firecracker microVM boot, and who builds it: the
  release of OpenDustpan, or the team?
- Does a Firecracker host keep a pool of started microVMs to make the time
  to the first step shorter? A microVM must still serve one claim only.
- Does the `process` sandbox need a user namespace that the host allows
  for users that are not root, and what does it do on a host that refuses
  it?
