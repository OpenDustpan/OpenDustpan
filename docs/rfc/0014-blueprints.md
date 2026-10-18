---
rfc: 14
title: Blueprints, self-service units with validated inputs
status: accepted
created: 2026-10-10
adr: 110, 111, 112, 113, 114, 115, 116, 117, 199
---

# RFC 14. Blueprints, self-service units with validated inputs

Status: accepted. ADRs 110 to 117 and ADR 199 record the decisions (see
"Outcome").

## Problem

A platform team owns a program: a Pulumi project or an OpenTofu module that
makes a database, a Kubernetes namespace or a service account the right way.
Other teams need many units of it, each with a few values of their own: a
name, a size, an owner, a list of allowed callers.

Today the platform team has two bad choices:

- Give each team the whole program, or the whole config file, and guard it
  with CODEOWNERS rules, Semgrep checks and review. Teams can then change
  anything, and the platform team reviews everything.
- Do every change for them, and become the queue.

Other products cover part of this:

- HCP Terraform and Terraform Enterprise have no-code provisioning. A module
  in the private registry becomes a form, and a person makes a workspace by
  filling in its variables. The values live in the service, not in git. The
  form shows every variable of the module, and validation is the module's own
  variable validation.
- Pulumi Cloud can make new projects from templates. The template copies code
  into a repository, and from then on the team owns the copy.

Neither gives a team a small, validated set of inputs that it owns, while the
platform team keeps the program and every other value.

## Proposal

A **blueprint** is a program that a platform team offers for self-service,
with a declared set of inputs.

### What a blueprint holds

- **Tool and program.** One tool, and a source (RFC 12) for the program: a
  repository, a directory and a version (a tag or a commit). Units pin a
  blueprint version.
- **Input schema.** The inputs that a consumer can set, as JSON Schema: types,
  enums, patterns, limits, required keys, descriptions for the form. JSON
  Schema covers JSON and YAML input files, and the console can draw a form
  from it.
- **Mapping.** Each input maps to the tool's own config: a Pulumi config key
  or an OpenTofu variable. Nothing is templated into program code.
- **Platform values.** Values that the platform team sets and the consumer
  cannot see or change: profiles (RFC 5), fixed config, runner labels
  (RFC 6).
- **Naming.** A rule that names the unit from its inputs, for example
  `tenants/{team}-{name}`.

### Units from a blueprint

- A consumer makes a unit from a blueprint and gives the inputs. The unit is
  a normal stack or workspace in its tool's model (ADR 13). The inventory
  records which blueprint and which version it came from.
- The consumer gets the **edit inputs** action on that unit. It does not get
  edit rights on the program, the platform values or the state. This is the
  boundary that CODEOWNERS rules try to draw on file paths. Here it is drawn
  on input keys.

### Where inputs live

The one-home rule of RFC 12 applies. The inputs of a unit live in one of two
places:

- **In OpenDustpan.** The console form or the API edits them. Each edit is an
  edit in RFC 13's sense, and edit policy decides about it.
- **In git, in any repository.** The inputs are a JSON or YAML file at a path
  in a source of the consumer's choice, for example
  `payments/infra/db-orders.yaml` in the payments team's own repository. The
  platform repository holds no copy. Edits are commits there, by people or by
  write-back.

### Validation

A blueprint can attach any of these. They run in this order, and the first
failure stops the edit or the job:

1. **Schema.** The backend checks the inputs against the input schema.
2. **Edit policy** (RFC 13). Who may set which input to which value, and
   whether it needs approval.
3. **Custom checks.** A command that the platform team gives, run by a
   runner before the plan (RFC 6, job steps). It can do anything: call an
   internal API, check a cost limit, check a name in a CMDB.
4. **The tool's own checks.** OpenTofu variable validation, Pulumi program
   checks, and resource policy (RFC 13) on the plan.

Inputs in git change without the API. So the backend runs steps 1 and 2 at
three times: on an edit through the API, on a webhook for a review request
that touches an input file (and posts the result as a status on that review
request), and again when a job starts. A file that fails never reaches a plan.

### Rendering

When a job starts, the backend resolves the inputs and the platform values,
checks them, and gives them to the runner as the tool's config: Pulumi config
values, or an OpenTofu `*.tfvars.json` file. The rendered values are not
committed anywhere.

- **Pulumi CLI.** The backend can also serve the rendered config through the
  stack config endpoint (RFC 12), so a `pulumi preview` on a workstation sees
  the same values.
- **OpenTofu.** As in RFC 5, a workstation run gets no rendered values. A unit
  from a blueprint runs on runners.

A team that wants real templating (code generated from templates) can do it
in a job step before the plan. The blueprint itself only maps inputs to
config.

### Versions and rollouts

- A unit pins a blueprint version. An upgrade to a new version is an edit, and
  edit policy decides about it.
- The platform team can roll a new version out to all units of a blueprint, or
  to a set of them, as one batch of jobs, with plans to look at before any
  apply.

## Options

1. **Blueprints as above** (the proposal).
2. **Do nothing.** Teams keep CODEOWNERS, Semgrep and review on the whole
   program.
3. **Inputs only in OpenDustpan, as in no-code provisioning.** Simpler. It
   loses the inputs-in-git case, and with it the teams that want git as the
   record (RFC 12).
4. **Code templates.** The blueprint generates program code for each unit.
   Each unit then has its own copy of the code, which drifts from the
   blueprint after the first change. Rejected as the default. A job step can
   still do it.

## Proposed glossary entries

**Blueprint**:
A program that a platform team offers for self-service, with an input schema,
platform values and a version. Units made from it are normal stacks or
workspaces.
_Avoid_: Template (Pulumi templates copy code), no-code module, product,
offering, golden path

**Input**:
One value that a consumer of a blueprint can set, declared in the
blueprint's input schema.
_Avoid_: Parameter, variable (which is OpenTofu's word), field

## Open questions

- Is "blueprint" the right word? It is short and not taken by either tool.
- Input files in YAML need a YAML library, which is a new dependency. HCL
  input files need `hashicorp/hcl`. Are JSON and YAML enough at first?
- How does a new blueprint version change the input schema (rename a key,
  add a required key), and who migrates the inputs of existing units?
- Can one input file hold the inputs of many units (one file per team)?
- Who owns a unit from a blueprint for access rules: the consumer team, the
  platform team, or both, each for its own actions? This depends on roles
  (RFC 2) and on RFC 4.
- Does a blueprint live in a namespace, and can units in other namespaces use
  it?

## Gaps from the competitor review

Added 2026-10-10 from the research doc "IaC and TACOS landscape". Not yet
worked into the proposal above.

- A share link to the form only, with no editor (Spacelift).
- Attach a blueprint to units that already exist; locked fields that reject a
  conflicting edit with a list of conflicts (Harness).
- Make a blueprint from an existing module by selecting attributes
  (Resourcely); questionnaires before provisioning; account vending
  (Gruntwork).
- A time to live as a blueprint field (RFC 43).

## Outcome

Accepted as ADRs 110 to 117, the Catalog and blueprints, on 2026-10-17, and
as [ADR 199](../adr/0199-file-sets-for-sources-with-no-git-commit.md), file
sets for sources with no git commit, on 2026-10-18.
[ADR 203](../adr/0203-job-tokens-no-change-plans-bulk-calls-forge-connections-and-locks.md)
decision 5 gives the read of the options of a blueprint form its own action.
A question of this RFC that these ADRs do not decide needs a new RFC.
