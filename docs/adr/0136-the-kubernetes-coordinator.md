# 136. The Kubernetes coordinator

Status: accepted · 2026-10-18 · adds to RFC 31 phase 2

Changed by:

- ADR 139 extends decision 9: client-go and the separate dustpan-runner
  binary.
- ADR 305 extends: the Kubernetes runner runs the jobs of the operator.

## Context

[RFC 31](../rfc/0031-kubernetes.md) phase 2 proposes a Kubernetes runner:
one pod for each job from a pod template, and a service account for each
unit, so that the workload identity of a cloud gives each unit its own
role. ADR 130 makes a runner a coordinator. ADR 133 decides the handoff of
a Kubernetes pod.

The project owner decided on 17 October 2026 (owner):

- On Kubernetes, a coordinator creates one Kubernetes Job for each job
  phase, with `backoffLimit: 0`.
- The pod gets its handoff credential by a TokenReview pull.
- The validation matrix for later tests: EKS, GKE, AKS, Alibaba ACK,
  Oracle OKE, k3s and STACKIT SKE. Serverless targets come later.

The project owner approved every decision of this ADR. The label "(owner)"
names a decision that the text of the owner of 17 October 2026 states.
The ports are in `pkg/runner` (`runner.Cluster`).

## Decision

1. **One Job for each claim (owner).** For each claim of a phase or a sweep,
   the coordinator creates one `batch/v1` Job (`runner.KubernetesJob`):
   - The name is `dustpan-` and 20 hex characters of the SHA-256 of the
     operation and the claim token (`runner.KubernetesJobName`). A retry of
     the create after a lost answer names the same Job, so one claim never
     gets two Jobs. A takeover is a new claim, so it gets a new Job.
   - `backoffLimit: 0` and `restartPolicy: Never`. Kubernetes never starts
     the launcher of one claim twice. The takeover rules of ADR 100 decide
     what happens after a failure, not Kubernetes.
   - `activeDeadlineSeconds` and `terminationGracePeriodSeconds` from the
     limits of the launcher (`runner.LimitsFor`). The launcher owns the
     deadline of each step. The deadline of the Job is a generous limit:
     the steps, their grace periods and the report, plus a fixed allowance
     of 40 minutes for the schedule, the image pull, the session and the
     source (ADR 134 decision 9).
   - `ttlSecondsAfterFinished` of 1 hour, so a person can read the pod for
     one hour after its end.
   - The labels `opendustpan.org/operation`, `opendustpan.org/claim` and
     `opendustpan.org/runner`.
2. **The pod.** The coordinator fills a pod template of the team:
   - An init container from `opendustpan/launcher:<release>` runs
     `dustpan-launcher copy --dest /dustpan/bin`, into an `emptyDir`.
   - The main container runs the image of the unit or of the pool, with
     the tools. Its command is the launcher (`launcher.Command`). It mounts
     the launcher binary read-only, an `emptyDir` for the work directory,
     and an `emptyDir` with `medium: Memory` and a size limit for the
     secrets directory.
   - `automountServiceAccountToken: false`, and a projected service
     account token with the audience `opendustpan-coordinator` and a
     lifetime of 10 minutes, the shortest that Kubernetes allows.
   - The service account of the pod (`runner.ServiceAccountFor`):
     - For a pool for jobs, the service account of the unit from `Work`,
       or the default of the Helm values when the unit names none. The
       coordinator runs the Job only when the allowlist of service
       accounts in its Helm values names it, and never as its own service
       account, also when the allowlist names it. So the workload identity
       of the cloud (IRSA, GKE Workload Identity, Azure Workload Identity
       and the others) gives each unit its own role, as RFC 31 proposes,
       and a unit cannot pick a service account that the team did not
       list.
     - For a pool for sweeps, the read-only service account of the Helm
       values of the coordinator. The `Work` of a sweep names no service
       account, and a `Work` of a sweep that names one fails.

     A refused service account starts no Job. The coordinator releases the
     claim with the reason, and the console shows it.
   - `runAsNonRoot`, `allowPrivilegeEscalation: false`, all capabilities
     dropped and the `RuntimeDefault` seccomp profile. A pod template that
     sets `hostNetwork`, `hostPID`, `hostIPC`, a `hostPath` volume or a
     privileged container fails the start of the coordinator.
3. **The TokenReview pull (owner).** The coordinator serves one HTTPS path,
   `POST /handoff`, on a cluster Service. Its certificate comes from a
   Secret, and its certificate authority reaches the pod in a ConfigMap.
   The launcher sends its projected token. The coordinator:
   - creates a TokenReview with the audience `opendustpan-coordinator`
     (`runner.Cluster.ReviewToken`),
   - reads the pod that the token names, with its owner Job and its
     labels (`runner.Cluster.Pod`),
   - checks that both name the same pod UID, namespace and service
     account, that the pod belongs to the Job of a claim that the
     coordinator holds, and that its labels name that claim
     (`runner.CheckHandoffPod`),
   - asks the server for the handoff token of the claim, answers it, and
     keeps no copy. A second request for the claim gets nothing.

   A refused request appends an audit entry of the event `handoff-refused`
   through the protocol, with the pod and the reason, and no token.
4. **The permissions of the coordinator.** The service account of the
   coordinator needs, in its namespace: create, get, list, watch and
   delete on Jobs, get on pods, and get on its Secret. In the cluster, it
   needs create on `tokenreviews`. It needs no other permission. The Helm
   chart of the coordinator creates exactly these roles. The Helm values
   name the service accounts of the Jobs (`runner.ServiceAccounts`): the
   allowlist and the default for a pool for jobs, and the read-only
   service account for a pool for sweeps. A Job never runs as the service
   account of the coordinator, because that account can create Jobs and
   read pods.
5. **The end of a Job.** The coordinator watches its Jobs
   (`runner.Cluster.JobEnded`):
   - A Job that fails before its launcher opened a session, for example on
     an image pull error or an unschedulable pod, makes the coordinator
     release the claim. Another runner can take it.
   - A Job that ends after the launcher opened its session needs nothing
     from the coordinator. The launcher reported the outcome, or the claim
     expires and the rules of a lost claim apply.
   - The exit code of the launcher tells the coordinator what to do
     (`launcher.DispositionFor`).
6. **Cancel and lost claims.** The launcher stops its step itself at a
   cancel (ADR 134). When the heartbeat answer lists a claim as lost, the
   coordinator deletes its Job in the foreground. The kubelet sends
   `SIGTERM` to the launcher, which sends `SIGINT` to the step, and
   `SIGKILL` follows after the termination grace.
7. **Many Jobs from one runner.** A Kubernetes runner usually has a pool
   concurrency above 1, because each claim runs in a Job of its own. A
   ResourceQuota of the namespace of the Jobs is the limit of the cluster.
   A Job that waits for a node holds its claim. The coordinator renews the
   claim until the session opens, for 30 minutes at most (ADR 131 decision
   7). When a renewal answers `sessionOpen`, the coordinator stops its
   renewals and leaves the Job alone: the launcher renews the claim.
8. **High availability of the coordinator.** Each replica of the
   coordinator is a runner of its own, with its own key and its own
   Secret. A replica that stops loses its claims after one lease, and the
   takeover rules apply. Its Jobs whose launchers hold a session go on: a
   launcher renews its own claim.
9. **No Kubernetes client library in the server.** The server never speaks
   to a cluster. Only the coordinator speaks to the Kubernetes API, through
   the port `runner.Cluster`. The project owner decides how the adapter of
   that port speaks to the API: with `client-go`, or with a small client
   of its own for about 6 calls. This design parks the choice for the
   owner and sets no default.
   The coordinator code does not land before the owner decides.
10. **The validation matrix (owner).** The e2e tests of the Kubernetes
    coordinator run on EKS, GKE, AKS, Alibaba ACK, Oracle OKE, k3s and
    STACKIT SKE. Each run plans and applies one OpenTofu unit and one
    Pulumi unit with the workload identity of the cloud, cancels a running
    apply, and takes over a claim after a delete of the coordinator pod.
    k3s runs in the CI of each pull request. The managed clusters run
    before each release. Serverless targets (for example AWS Fargate, GKE
    Autopilot and Azure Container Instances) come later.

## Consequences

- A team installs one Helm chart in one namespace. The coordinator needs
  no inbound port from outside the cluster, only the Service for its pods.
- Each phase is a fresh pod. Nothing of one phase stays for the next, and
  a takeover never sees the files of the claim before it.
- The time from a claim to the first step includes the schedule of a pod
  and the pull of an image. A team keeps its images on the nodes to make
  it short.
- GKE Autopilot and other platforms that refuse some pod settings may need
  their own template rules. The matrix shows it.

## Open questions

- Does the coordinator binary ship apart from the server binary, as
  `dustpan-runner`, or as a role of the `opendustpan` binary? This design
  uses `dustpan-runner`, with no SQL driver.
- How does the coordinator get its serving certificate: from
  cert-manager, from a Secret that the Helm chart makes, or from a
  certificate that it makes and puts in the ConfigMap itself?
- Does the Kubernetes operator of RFC 31 phase 3 share the chart and the
  binary of the coordinator?
