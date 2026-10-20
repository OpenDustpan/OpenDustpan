# 256. The server enforces resource policy, and the Pulumi adapter serves no required policy packs

Status: accepted · 2026-10-20. Decided by the project manager under the owner's delegation of 20 October 2026. Applies ADR 250 and ADR 222 to the policy packs of the Pulumi CLI

## Context

Decided by the project manager under the owner's delegation of 20 October
2026, with the owner's decision rule.

The Pulumi CLI can run the policy packs that its backend requires. We read
the CLI source (`pulumi/pulumi`, Apache-2.0, `master` at commit
`1ae86eef58a0`, 2026-10-20):

- The answer that creates an update (`begin-update`, or the older create
  routes of `preview`, `update`, `refresh` and `destroy`) holds
  `requiredPolicies`. Each entry has a name, a display name, a version, a version tag,
  a config, the `environments` and a `packLocation` URL
  (`sdk/go/common/apitype/policy.go`).
- The CLI downloads each pack from `packLocation` with a plain `GET` and
  no `Authorization` header. It unpacks it, installs its dependencies
  with the language tool (for example `npm` or `pip`), and runs it on the
  machine of the user, with the credentials and the environment of the
  user (`pkg/backend/httpstate/policypack.go`).
- The wire has no hash and no signature of a pack. The CLI checks none.
- The CLI reports results only as engine events of the update
  (`policyEvent` and the summary events).
- An empty or absent `requiredPolicies` does nothing. This is the behavior
  of the server today.
- `pulumi policy publish`, `enable`, `disable`, `ls` and `rm` use routes
  under `/api/orgs/{org}/policypacks` and `/api/orgs/{org}/policygroups`.

So a required pack is code that the server chooses and that the CLI runs
with the rights of the person. The person controls the CLI. The person can
skip the pack with a changed CLI, with another backend, or with a write
through the API. A check that the person controls does not stop the
person.

ADR 194 decision 8 and ADR 226 decision 7 already make the results of a
resource policy in a runner job a fact, and the built-in gate
`builtin/mandatory-resource-policy` denies a mandatory failure. The server
never runs a resource policy (ADR 194 decision 8).

## Decision

1. **The gate engine is the control.** Resource policy that must hold is a
   gate on a stored plan (ADR 224, ADR 226 decision 7). A gate runs on the
   server, at a decision point that the person cannot skip. A check on the
   machine of the person is never a control. A CLI-driven `pulumi up`
   gets no server-side resource-policy check, because `state.write` has no
   plan facts.
2. **No required packs in the answer of an update.** The Pulumi adapter
   keeps `requiredPolicies` absent in each answer that creates an update.
   It serves no `GET /api/stacks/{org}/{project}/{stack}/policypacks` and
   does not announce the capability `stack-policy-packs`.
3. **No policy pack routes.** The adapter serves none of the routes under
   `/api/orgs/{org}/policypacks` and `/api/orgs/{org}/policygroups`. A
   `pulumi policy` command that needs them fails with the error of the
   CLI for a missing route.
4. **The escape hatches stay.** A team runs a pack with
   `--policy-pack` on a workstation or in its own CI, and in a step of a
   runner job, where the results become the resource policy report of the
   stored plan. A write from a person's own CLI meets the gates of
   `state.write` (ADR 224 decision 16). A regulated team that wants no
   unchecked write from a person's CLI adds a gate at `state.write` that
   denies `source.kind` `cli`, so that changes go through runner jobs.
5. **A later change needs an RFC.** If served packs come back as fast
   feedback on a workstation, the RFC must keep them advisory, and it must
   answer three points: the integrity of the pack (the wire has no hash, so
   the server must store the pack and serve it from a signed URL of its
   own, never a URL of another host), the code that the server sends to
   the machine of a person, and the audit entry of each served pack.

Benchmark: AWS evaluates the service control policies of AWS Organizations
in the service, not in the client. Google Cloud does the same for the
constraints of its organization policy. Kubernetes admission control runs in the API server. PCI DSS and
SOC 2 ask for controls that the person under control cannot turn off.

## Consequences

- A person sees a policy failure of a CLI-driven `pulumi up` only when a
  gate refuses the write, or when the person runs the pack locally. The
  user documentation says this, and names `--policy-pack`.
- The server sends no code to the machine of a person. This removes a
  path where a change of a stored pack, or of its URL, runs code on every
  workstation.
- A team that moves from a service with required packs keeps its packs. It
  runs them in runner jobs, where a gate reads the results.
