---
rfc: 124
title: "Code for unmanaged resources: a verified floor in each language, and a placement engine that fits the code into the team's project"
status: discussing
created: 2026-11-07
---

# RFC 124. Code for unmanaged resources: a verified floor in each language, and a placement engine that fits the code into the team's project

Status: discussing. Design only. Nothing in this RFC is decided, and no code
exists for it. It revises the "write code" half of RFC 47. The owner answers
the questions at the end (ADR 15). Until the owner decides, ADRs 163 to 165,
ADR 343 and ADRs 120 to 128 stay as they are.

The file name keeps the slug of the task. The text uses the glossary words:
**unmanaged resource**, **finding**, **Sweep**, **review request**,
**change order**, **write-back**. "Discovery" is a word to avoid.

## Summary

The owner's words, 7 November 2026: "Codegen is impossible to solve
perfectly. We can propose a basic resource shape in each language, and we can
have support for Dusty to propose a PR integrate it in the user's project.
This is hard because resources are rarely just a single `resource` block."

This RFC splits the problem in two and states for each part what is
guaranteed and what is a heuristic.

1. **The floor.** For one finding, a job on a runner writes one resource with
   an import directive: an OpenTofu `resource` block and an `import` block, or
   a Pulumi resource with the `import` option in TypeScript, Python, Go, C#,
   Java or YAML. The job drops computed attributes and attributes that a
   no-change plan shows are not necessary, and gives a stable name. A value
   goes into Git only when the job can show that it is not a secret
   (default-deny); every other value becomes a reference. The job refuses to
   deliver the code unless a plan shows "import 1, change 0" for the new
   resource and no change, no destroy and no protection change to any other
   resource of the unit. The plan check is the acceptance test of the
   result. It is not a security layer.
   The runner sends **typed edits**, not text. The server checks their
   content and renders the code with one escaping encoder for each
   language.
2. **The ceiling.** A placement engine proposes where the code goes in the
   team's project: an entry in an existing `for_each` map, a new module call,
   an entry in the list that a Pulumi loop reads, an argument that comes from
   a variable, a tfvars file or a profile. It reads the provenance that the
   tools already record (OpenTofu instance addresses, Pulumi source positions
   and stack traces), inverts simple templates, infers references by value
   equality, and ranks the candidates. Every candidate that it offers passed
   the same plan check on the files that it edits. The ranking is a
   heuristic. The check is not.
3. **Dusty** opens the review request of a change order with the chosen
   candidate, the other candidates, the evidence and the plan. Dusty picks
   and explains. It writes no code that skips the check.

Most of the floor exists in the tools. The new parts are the minimization,
the secret references, the names, the check, and the placement engine. The
section "Is this a paper?" says what is new against the published work, and
how to measure it.

## Problem

- ADR 165 decision 6 defines the action `write-code` on a finding: "Starts a
  job on a target unit that generates the code of the resource and delivers
  it as a review request." ADR 165 decision 8 does not offer it until the job
  model has the operation. Until then the console shows
  `tofu plan -generate-config-out` and `pulumi import --generate-code`.
- ADR 165 asks the job model for "code generation delivered by write-back: a
  write-back that adds a new file of code". ADR 101 decision 4 allows only
  config and variable files.
- The tools' own generators write code that a team does not want to merge,
  and sometimes code that does not plan clean (section "Survey"):
  - OpenTofu writes every required and optional attribute, with `null #
    sensitive` for a secret. It writes no reference to another resource. Its
    documentation says that the generated code can fail with "Conflicting
    configuration arguments", and calls the feature experimental.
  - `pulumi import` writes a secret as `secret("<the plain value>")`, so the
    plain value goes into the source file.
- Code that a team writes is rarely one block. Inputs come from tfvars files,
  profiles, `locals`, modules, components, `for_each`, `count`, loops and
  generator functions. A new block next to a `for_each` of 40 buckets is
  correct and still wrong: the next change to the loop misses it.
- RFC 123 (in review) proposes sweep report version 2, with an `iac` entry
  for each tool: the IaC type and the import ID or identity. Without it, a
  finding has the cloud type only, for example `AWS::S3::Bucket`, and the
  job must map the type itself.

## What is decided already

| Source | Rule | What it means here |
| --- | --- | --- |
| RFC 6, ADR 120 decision 2 | The server never runs an infrastructure tool. Dusty has no command tool. | Code generation and the plan check run on a runner. Dusty does not generate code in the server. |
| ADR 100, ADR 101 | A job belongs to one unit. Write-back runs in the server and edits only config and variable files (decision 4). | A code generation job names one target unit. Code write-back is a change to decision 4. |
| ADR 165 decisions 6 and 8 | `write-code` starts a job on a target unit and delivers a review request. It needs `namespace:triage-unmanaged` and `unit:write`. | This RFC designs that job. |
| ADR 163, ADR 164 | Findings come from the state check and cloud sweeps. A sweep report is untrusted. | The finding is untrusted input to the job. |
| ADR 343 | A tool import is a runner job. A pool may have more than one purpose. | The job runs on a pool with the purpose `jobs`, with the plan credentials of the unit. |
| ADR 124 | Agents never see decrypted secrets. | Dusty reads masked evidence only. The generated code holds no secret value. |
| ADR 125, ADR 128, ADR 299 | An agent change goes through the job model. The deliver phase of a write-back job of an agent waits for the person's confirmation. A review request carries a change order. | Dusty's part is a change order with a write-back job. |
| ADR 122 decision 14 | The tool catalog is a contract, with 8 to 12 tools. | Dusty gets no new tool. `propose_change` gets an operation. |
| ADR 299 decisions 20 and 24 | One credential rule for every plan. Each unit has declared paths. | The check uses the plan credentials. An edit of a shared path plans each unit that declares it. |
| ADR 250 | Defense in depth for every input from outside the process. | Section "Security". |
| CLAUDE.md, "Working in this repo" | Ask before adding a dependency. No GPL, AGPL or BUSL code. | Section "What we reuse". |

## Survey: what exists

Checked on 7 November 2026 in the source of OpenTofu (`main` at `90d4525`),
the source of the Pulumi CLI (`master` at `6bbe25d`), and the documentation
and repositories named. "Not found" means that the search found nothing.

### Code generators for one resource

| Tool | License | What it does | Gaps for this RFC |
| --- | --- | --- | --- |
| OpenTofu `tofu plan -generate-config-out` | MPL-2.0 | For each `import` block with no `resource`, reads the resource through the provider and writes a `resource` block. `internal/genconfig` writes each attribute that the schema marks required or optional, and skips computed-only attributes. A sensitive attribute becomes `null # sensitive`, a write-only one `null # write-only`. An empty string of an optional attribute becomes null ("SHAMELESS HACK", for the legacy SDK). The documentation marks the feature experimental since 1.6. Import blocks take `for_each` (documentation of 1.13). | Writes optional-and-computed attributes, so a plan can fail on conflicting arguments (documented). No references, no variables, no minimization. A secret becomes null, so the plan of the generated code changes the secret. The package is `internal`, so no other module can import it. |
| Terraform 1.5 and later; `terraform query` from 1.14 | BUSL-1.1 | The same generator. From 1.14, list queries (RFC 123); its documentation describes code generation for their results, which this RFC did not check. | BUSL. Prior art only. We do not run, link or ship it (RFC 53 is a draft). |
| `pulumi import --generate-code` | Apache-2.0 | Imports by type, name and ID, or from an import file with `nameTable`, `parent`, `provider`, `logicalName`, `properties` and `component`. `pkg/importer` builds a PCL program from the inputs that the provider read, adds the options `parent`, `provider`, `dependsOn`, `protect`, `ignoreChanges` and `import`, and turns a value equal to the ID of a resource in the name table into a reference. The language plugin turns it into a program (`GenerateProgram`). `--preview-only` does not import. `--from terraform` reads Terraform state through a converter plugin. | A secret becomes `secret(<the plain value>)` (`generateValue`, case `value.Secret()`). It writes every input that Read returned, defaults included for some providers. No placement. |
| aztfexport (Azure) | MPL-2.0 | Lists Azure resources, imports each one, writes HCL with `tfadd`. Its stated goal: `terraform plan` shows no diff. `tfadd` has a partial mode that leaves out optional-and-computed attributes with cross-property constraints. | Azure only. Runs the Terraform CLI through `terraform-exec`. Its README says that the output is not meant to be complete. We take the idea of the partial mode, and the plan as the acceptance test. |
| Terraformer | Apache-2.0 | Lists with the cloud SDKs, reads with the provider, writes HCL and state. | Archived since 16 March 2026, pins Terraform 0.12 (RFC 123). Prior art only. |
| former2 | MIT | In a browser, reads AWS with the SDK and writes CloudFormation, Terraform and other formats (its README lists them). | AWS only, a browser tool, no releases (RFC 123). Prior art only. |
| CDK for Terraform (`cdktf convert`) | MPL-2.0 | Converted HCL to CDK languages. | Archived by HashiCorp on 10 December 2025. Not used. |
| pulumi-converter-terraform | Apache-2.0 | Converts an OpenTofu or Terraform program, with modules, `count` and `for_each`, to a Pulumi program through PCL. | Translates code, does not generate it from state. Prior art for a translation that keeps the structure of loops. |
| Closed services: AWS CloudFormation IaC generator, Pulumi Cloud import to code, HCP Terraform Search, Firefly, ControlMonkey | closed | Find resources and write code with import directives. | No source. Their documentation shows the same floor: one block for each resource. No placement into an existing loop was found in their documentation. |

### Facts that change the design

- **Pulumi state records where each resource came from.** `ResourceV3` has
  `sourcePosition` and `stackTrace` (`sdk/go/common/apitype/core.go`). Two
  resources of one type with the same source position come from one loop or
  one function. A frame inside a component constructor names the component.
  So the runtime already answers "which loop made this resource", in every
  language, with no parser of that language. A program that does not send
  source positions leaves the field empty, and the engine then has less
  evidence.
- **OpenTofu state records the loop of each resource.** An instance address
  holds the key (`aws_s3_bucket.this["logs"]`) and the module path
  (`module.buckets["a"].aws_s3_bucket.this`). `tofu show -json` of a plan
  gives, for each attribute of each resource in configuration, its
  `references` or its `constant_value`, and the `for_each_expression` of the
  resource. So the expression that feeds an attribute is known without a
  parser of our own.
- **A plan with an import is a non-destructive check.** An OpenTofu plan with
  an `import` block, and a Pulumi preview with the `import` option, read the
  resource and compare it with the code, and write no state.
- **The exit code is not enough.** `tofu plan -detailed-exitcode` returns 2
  for a plan with an import. The check reads the JSON plan, not the exit
  code.

### Research

Each item names its source. The column "Use here" says what this RFC takes.

| Work | What it shows | Use here |
| --- | --- | --- |
| Weiss, Guha, Brun. "Tortoise: Interactive System Configuration Repair." ASE 2017, pp. 625-636; arXiv:1709.05366 | An administrator fixes a machine in the shell. Tortoise synthesizes a minimal edit of the Puppet manifest that matches the fix, and ranks the candidates. On 42 scenarios, people chose the first candidate 76% of the time; the mean rank of the human repair was 1.31. | The closest problem: change made outside the code, edit the code to match, minimal edit, ranked candidates. We use their ranking by edit size. Tortoise uses a solver over a small language; we use the tool's plan as the check. |
| Yang, Guan, Nicolet, Paulsen, Dodds, Kroening, Chen. "NSync: Automated Cloud Infrastructure-as-Code Reconciliation with AI Agents." arXiv:2510.20211; ACM, doi 10.1145/3832121 | Finds drift from cloud API traces, and an LLM agent edits Terraform code to match. Its test pipeline injects drift, and a reconciliation passes when `terraform plan` shows no planned changes. 372 drift scenarios in 5 projects; pass@3 from 0.71 to 0.97 against its baseline. | The same acceptance test. Their edits change resources that exist in code. Ours add a resource that has no code. An LLM may propose candidates (slice 8), and the check stays the same. |
| Foster, Greenwald, Moore, Pierce, Schmitt. "Combinators for bidirectional tree transformations: a linguistic approach to the view-update problem." TOPLAS 29(3), 2007 | A lens has a `get` (source to view) and a `put` (changed view back to source), with laws that make a round trip exact. | The frame. An IaC program is a `get` from code and inputs to desired state. Codification is a `put` of a new resource. The tools have no `put`. The plan is the check of the round-trip law for one case. |
| Plotkin 1970 and Reynolds 1970 (anti-unification); Bulychev, Kostylev, Zakharov. "Anti-unification algorithms and their applications in program analysis." PSI 2009, LNCS 5947, 2010; Bulychev, Minea, "Duplicate code detection using anti-unification" (Clone Digger) | The most specific template of two terms: equal parts stay, different parts become holes. Used to find clones. | The instances of one loop are clones of one template. Their anti-unifier names the attributes that vary (holes) and the ones that do not. |
| Rolim, Soares, D'Antoni, Polozov, Gulwani, Gheyi, Suzuki, Hartmann. "Learning syntactic program transformations from examples" (Refazer). ICSE 2017, pp. 404-415 | Learns a rewrite from edits as input-output examples, with a ranking. 84% of 56 repeated-edit scenarios, from 2.9 examples on average. | The siblings of a resource are examples of how the team writes it. |
| Gulwani. "Automating string processing in spreadsheets using input-output examples" (FlashFill). POPL 2011 | Synthesizes string programs (concatenations of constants and substrings) from a few examples. | Names and tags often follow a string template, for example `"${var.env}-${each.key}"`. A small string language of that kind inverts them. |
| Santolucito, Zhai, Dhodapkar, Shim, Piskac. "Synthesizing configuration file specifications with association rule learning." OOPSLA 2017, doi 10.1145/3133888 | Learns rules from a corpus of configuration files (ordering, correlation, type, missing entry). | Learn from the namespace which attributes of a type the team sets from variables or references. |
| Zeller, Hildebrandt. "Simplifying and isolating failure-inducing input." TSE 28(2), 2002 (delta debugging) | Minimizes an input against a test oracle in few test runs. | Minimize the attribute set of a resource against the plan oracle. |
| Sokolowski, Spielmann, Salvaneschi. "Automated Infrastructure as Code Program Testing" (ProTI). IEEE TSE 2024 | Tests Pulumi programs with mocked resources and generated values. | A way to evaluate a Pulumi candidate offline before the cloud plan (later). |
| Kon et al. "IaC-Eval." NeurIPS 2024 Datasets and Benchmarks; IaC-Eval v2, arXiv:2607.20478; TerraFormer, arXiv:2601.08734; DPIaC-Eval, arXiv:2506.05623; Multi-IaC-Eval, arXiv:2509.05303; the error taxonomy of arXiv:2512.14792 | LLMs write IaC from text, with plan or deploy checks. TerraFormer also mutates existing configuration. | Evidence that model-written IaC fails often enough that a check must gate it. None of them starts from a resource that exists in the cloud and has no code. |

**Not found:** a published method or tool that places generated code for an
existing cloud resource into an existing loop, module or variable file of a
project. The search for this RFC was not complete. The claim is "not found",
not "does not exist".

## Part A. The floor

### A1. Where it runs

- A new job operation `codify` on a target unit. It is a plan-like job: it
  runs on a runner of the unit's pool with the plan credentials of the unit
  (ADR 299 decision 20). It changes no state and no cloud resource.
- Input: the finding IDs (1 to 50, of one namespace), the target unit, and
  for each finding the tool type and the import ID or identity. These come
  from the `iac` entry of sweep report version 2 (RFC 123), from a mapping
  table in the runner when the report has none, or from the person.
- Output, the **codification result**: a set of **typed edits** (section
  B9), the evidence for each resource, the SHA-256 of each file that the
  check planned, and the result of the check. The runner sends no text of
  code. The server checks the edits, renders them with its own encoders and
  stores the result as a job artifact. Without write-back of code
  (question 1), the console shows the rendered diff to copy.

### A2. The pipeline for OpenTofu

1. **Copy.** The runner checks out the code location of the unit at the
   commit of the job, in the sandbox.
2. **Generate.** It writes one `import` block for each finding, with
   `to = <type>.<stable name>` and `id` (or `identity` on OpenTofu 1.12 and
   later), and runs `tofu plan -generate-config-out=<scratch file> -out=<plan>`.
3. **Read the plan.** `tofu show -json <plan>` gives the values that the
   provider read, sensitive values included. They stay in the memory of the
   job and in the sandbox. They are never uploaded.
4. **Secrets.** Section A4.
5. **References.** Section A5.
6. **Minimize.** Section A3.
7. **Name.** Section A6.
8. **Typed edits.** Turn the result into typed edits (B9) and render them
   with the shared encoders. `tofu fmt` must leave the rendered files
   unchanged; a difference fails the job, because the server renders the
   same text.
9. **Check.** Section A7, on the rendered files.

### A3. Drop computed and default attributes

The plugin protocol gives no default values: a provider applies its own
defaults inside Plan. So "is this a default?" has one exact answer: the
plan. The minimizer asks it.

1. OpenTofu already drops computed-only attributes.
2. Drop each null value, each empty collection and each empty nested block of
   an optional attribute.
3. **First cut (the partial mode of `tfadd`).** Drop every attribute that is
   optional and computed, and every attribute that the schema marks
   deprecated when its replacement is set.
4. **The oracle loop.** Plan. For each new resource whose plan shows a change
   on an attribute that step 3 dropped, put that attribute back. A
   "conflicting arguments" error names its attributes: drop the one that
   step 3 did not drop. Plan again. Stop when the plan of every new resource
   is a no-op, or after 5 rounds.
5. One round is one plan for all the resources of the job, not one plan for
   each attribute. Delta debugging (Zeller, Hildebrandt) bounds the rounds
   in the worst case. The usual number of rounds is not measured. Slice 0
   measures it.
6. A resource that does not converge keeps the full output of OpenTofu, and
   the result marks it **not verified**. The job never offers a not-verified
   resource for write-back.

What this guarantees: the kept attributes are a set for which the plan is a
no-op. It does not guarantee the smallest such set, and it does not
guarantee that a dropped attribute keeps its value after a later provider
upgrade changes a default. The review request says so.

### A4. No secret reaches Git: default-deny for values

A secret value never enters the generated code, the typed edits, the
evidence, a log or Dusty's context. Not even as ciphertext. This includes a
secret in an attribute that the provider does not mark sensitive, for
example a Lambda environment variable, an ECS container definition or user
data.

The rule is **default-deny**: a value becomes a literal in Git only when the
job can show that it is not a secret. Every other value becomes a reference.
An unclear value is never written.

1. **Values that may be literals.** A value is written as a literal only
   when one of these holds, and the checks of step 3 pass:
   - its schema type is a boolean or a number;
   - it became a reference to another resource, a variable that the team
     already has, a local or a module output (A5, B6), so no new value is
     written;
   - its attribute is on the **non-secret list** of its resource type, and
     the value has the shape that the list gives for it. The list is data in
     the runner repository, reviewed as code. An entry names one attribute
     of one type and a shape: an identifier of the provider (an ARN, a
     resource ID), a region, a CIDR, a name of at most 255 characters from
     a fixed character set, or a value from a closed set (for example
     `STANDARD`, `GLACIER`). A map, a list, a JSON document, a free-form
     string and an attribute that is not on the list are never on it by
     default. Tag keys may be on it; tag values only with a shape.
2. **Values that are references.** Every other value, and every value of an
   attribute that is one of these, is a reference:
   - the schema marks it sensitive or write-only, or the plan marks it
     sensitive, or Pulumi gives it the secret signature (exact);
   - its name or any key on its path matches the credential words: the rule
     of ADR 101 decision 7 (`job.IsCredentialEnv`), and also `password`,
     `passwd`, `pwd`, `passphrase`, `secret`, `token`, `key` as a word part
     (`api_key`, `access_key`, `private_key`, `signing_key`), `credential`,
     `cert` with `private`, `auth`, `bearer`, `session`, `cookie`,
     `signature`, `salt`, `seed`, `dsn`, `connection_string`, `conn_str`
     and `webhook` (heuristic, a widening only; default-deny already covers
     these values);
   - a free-form string, a map or a JSON document: environment variables,
     container definitions, user data, scripts, policy documents.
3. **The checks of a literal, on the runner and again in the server.** A
   literal fails, and becomes a reference, when:
   - its keyed digest equals the keyed digest of a known secret (step 6);
   - a secret pattern matches it (the pattern set of the project's secret
     scan in CI);
   - its Shannon entropy is above the limit for its length (a number that
     slice 1 sets from the benchmark corpus).
4. **The forms of a reference.**
   - OpenTofu: `var.<name>` and a `variable` block, with `sensitive = true`
     for a value from step 2 or step 3, and `ephemeral = true` for a
     write-only attribute where the tool version has it.
   - Pulumi: a stack config key read with `config.requireSecret` or
     `config.require` (or `${config.<key>}` with `secret: true` in YAML).
5. **Where the value goes.** Never to Git.
   - When a profile secret of the unit equals the value (compared by keyed
     digest on the runner), the reference names that profile key.
   - Else the runner sends the value, sealed, to a new **profile draft** of
     the unit (the profile drafts of ADRs 200 to 209), and the change order
     names the draft. A person publishes the draft; it is not live before.
     Dusty never reads the draft's values (ADR 124).
   - When the draft cannot be written (no profile attached, no right to
     write one), the resource is **not codified**, with the reason. The job
     writes nothing for it.
6. **The digests.** The server gives each job a random digest key. The
   runner sends the keyed digests (HMAC-SHA-256) of every value that step 2
   or step 3 turned into a reference, and of every sensitive value that the
   plan read. The server computes the same digest of every literal of the
   typed edits, and of every secret of the profiles of the namespace, which
   it can decrypt. One equal digest refuses the whole edit set. The key
   lives as long as the job, so the digests are useless after it.
7. **The check needs the values.** For the plan check only, the runner
   passes each referenced value as an input of the plan inside the sandbox
   (an environment variable of the plan step, masked in the log as a job
   secret is). It writes no file with it.
8. **The upload guard.** Before upload, the runner searches the typed edits
   and the evidence for each value from step 2, step 3 and the plan's
   sensitive values, plain and in its JSON, HCL, URL and Base64 forms. One
   hit fails the job, and the job uploads no edit. It also fails on
   `secret(` with a literal argument.
9. **Write-only attributes** (for example a password that the provider never
   returns): the job writes the reference, and the evidence says that the
   plan cannot compare it.

What this guarantees and what it does not: a value that is not on the
non-secret list never becomes a literal. A secret can still reach Git only
through an entry of the non-secret list whose shape a secret fits, and that
passes the pattern, entropy and digest checks. The list is the residual
risk, so it is short, reviewed, and grows by review only.

### A5. References to resources that exist

- The job builds an index of the values of the unit's state: `id`, `arn`,
  `name`, `self_link` and the root module outputs, for each managed
  resource.
- A literal string of a new resource that equals exactly one indexed value
  becomes a reference to it (`aws_vpc.main.id`). Two or more matches keep
  the literal, and the evidence names the candidates.
- For Pulumi, the import file puts the existing resources of the stack into
  `nameTable`, and `pulumi import` writes the references itself (fact above).
- New resources of the same job reference each other the same way. This
  orders a bucket before its policy.
- The plan checks each substitution, as it checks every other edit.

### A6. Stable names

A name must be the same each time the same finding is codified, so that a
second run does not make a duplicate.

- **Source of the name**, in order: the `Name` tag or label, the `name`
  attribute, the last segment of the import ID.
- **Form.** Lowercase ASCII letters, digits and `_`, at most 64 characters,
  starting with a letter (`r_` in front when not). Pulumi logical names use
  `-`. Language variables use the case of the language
  (`logsBucket`, `logs_bucket`, `LogsBucket`).
- **Collision.** When the name is in use in the unit, add `_` and the first 6
  hexadecimal digits of the SHA-256 of the import ID.
- **No order.** The name does not depend on the order of findings.
- The name function has a version. The finding stores the name and the
  version that the job used.

### A7. The check: "no changes"

The check is the acceptance test of every codification, the floor and the
ceiling. It checks the result. It is not a security layer (section B9).

- **OpenTofu.** On the edited copy, `tofu plan -out` and `tofu show -json`.
  Pass when every item holds:
  - **The new resource.** Each new address has the action `no-op` with an
    `importing` block, its `before` and `after` are equal, and it has no
    `previous_address`.
  - **The frame.** Every other `resource_changes` entry has the action
    `no-op`. No `delete`, no `create`, no `update`, no replace in either
    order (`delete` with `create`), and no `forget`. No `previous_address`
    on any entry: the edits add no `moved` or `removed` block, and an
    existing `moved` block does not start to apply.
  - **Not a destroy.** The plan mode is normal, not destroy, and the unit
    keeps every resource that it had.
  - **Protection unchanged.** For each existing resource, `prevent_destroy`,
    `create_before_destroy`, `ignore_changes` and `replace_triggered_by` in
    the configuration JSON are equal before and after the edits. The new
    resource gets the protection of question 5.
  - No output changes, no new `check` failure, and no error diagnostics.
- **Pulumi.** `pulumi preview --json` with the `import` options. Pass when:
  - each new resource has the step `import` with no detailed diff;
  - every other step is `same`: no `create`, `update`, `delete`, `replace`,
    `create-replacement`, `delete-replaced` or `discard`;
  - `protect`, `retainOnDelete`, `deleteBeforeReplace`, `replaceOnChanges`
    and `ignoreChanges` of each existing resource are equal in state and in
    the preview, and no alias is added;
  - no stack output changes.
- **The check plans what it shows.** The runner renders the typed edits with
  the same encoders as the server (section B9) and reports the SHA-256 of
  each rendered file. The server renders the same edits and compares. A
  difference refuses the edits.
- **The plan is a secret.** The plan file and the plan JSON hold every value
  that the provider read, sensitive values included. They are job plans of
  ADR 102: the `secret` flag is set, an item key seals them at rest, and a
  person sees only the masked readable plan. An unmasked read needs
  `unit:read-secrets`. Dusty and the evidence get only masked summaries. The
  sandbox and its copies end with the job.
- **Every unit that the edits touch.** An edit of a path that another unit
  declares (ADR 299 decision 24) plans that unit too, with the same rule.
  The first slices edit only the unit's own code location (question 6).
- **After the merge.** The plan of the review request (ADR 299) runs the
  same check again on the merge result. The edits came from a runner and a
  cloud read, so that plan runs as the plan of an untrusted source, in the
  sandbox of the plan of RFC 122 (its step 4), with the credentials that
  RFC 122 gives such a plan. An apply that imports is followed by
  a plan that must show no changes (`-detailed-exitcode` 0, or
  `pulumi preview --expect-no-changes`). The finding resolves when the index
  shows the key in the unit (ADR 165).

What the check proves, and what it does not:

| Proved, at the time of the plan | Not proved |
| --- | --- |
| Applying the edits imports the resource and changes no attribute of it. | That the code is what the team would write. |
| No other resource of the planned units changes. | Units that use the same files and that OpenDustpan does not know. |
| The provider and the code agree on the resource, under the provider's own diff rules. | Attributes that the provider does not compare: write-only attributes, `ignore_changes`, attributes that a provider normalizes. |
| | That the cloud does not change before the merge. The plan of the review request runs the check again. |
| | That a provider upgrade keeps the same defaults. |

### A8. Pulumi in six languages

1. Read the language from `runtime` in `Pulumi.yaml`.
2. Write an import file: type token, stable name, ID, the provider of the
   stack, and `nameTable` with the existing resources.
3. Run `pulumi import --file <file> --preview-only --generate-code
   --out <scratch>` against the stack, with the language **YAML** as the
   working form. YAML is data: a YAML library edits it, with no parser of a
   general language.
   - Whether `--preview-only` gives code is not confirmed. The code path
     generates from the snapshot of the operation. If it does not, the job
     imports into a copy of the state in a local backend in the sandbox, and
     never into the real stack. Slice 2 tests this first.
4. Apply A3 to A6 on the YAML. A3 uses the preview with the `import` option
   as its oracle: a dropped input that the preview shows in the diff goes
   back.
5. Turn the YAML into a typed edit `add-pulumi-resource`: the type token,
   the logical name, the inputs as a typed value tree with the schema type
   of each property, and the options. `pulumi convert --from yaml
   --language <language>` is a reference only: the runner compares its
   output with the shared encoder's output in tests, and never ships it.
6. The encoder of the language renders the declaration: a new file that the
   entry file imports (TypeScript, Python), or a new function in a new file
   and one call in the entry point (Go, C#, Java). YAML projects get a new
   entry under `resources`.
7. Check with A7, on the rendered files.

A loop or a component in Pulumi cannot take an `import` option for one new
item without a change to the loop's code. Section B5 handles that case.

### A9. What we reuse and what we build

**Reuse (run as a process on the runner, never linked into the server):**

- The team's own `tofu` and `pulumi` binaries, which the runner already runs.
  OpenTofu's generator and `pulumi import` write the first draft, which the
  job turns into typed edits. `pulumi convert` is a test reference for the
  Pulumi encoders. We use their licenses as a user does: we run the
  programs; we copy no code.
- The ideas of `tfadd` (partial mode) and aztfexport (the plan as the goal),
  with no code.

**Reuse (a library):** an HCL parser that keeps comments and layout,
`github.com/hashicorp/hcl/v2` with `hclwrite`, MPL-2.0. It is the only
practical choice for HCL in Go. The runner needs it to read the project.
The server needs it to apply an edit into an existing HCL file (a new map
entry). It needs the owner's approval (question 3).

**Build:**

- The `codify` job operation, its result format, the evidence, the API and
  the console view (server).
- The typed edit schema, its checks, and one escaping encoder for each
  target language (B9), shared by the server and the runner.
- The minimizer, the secret referencer and the upload guard, the namer, the
  reference index, the plan-JSON checker (runner).
- The placement engine of part B (runner).
- The write-back of code (server), behind question 1.

**Not used:** Terraform (BUSL-1.1), CDK for Terraform (archived), Terraformer
(archived), any copy of `internal/genconfig`.

## Part B. The ceiling: fit the code into the team's project

### B1. The problem, stated exactly

Let `P` be the code of a unit, `E` its inputs (tfvars, profile values, stack
config), `S` its state and `C` the cloud. The tool computes the desired state
`D = eval(P, E)`, and a plan computes the actions `plan(D, S, C)`.

For an unmanaged resource `r`, find an edit `e` of `P` or `E`, and an import
directive for a new address `a`, so that:

- `plan(eval(P + e, E + e), S, C)` is "import `r` at `a`, no change to `r`" and
  no-op for every other address (the frame condition), and
- `e` is the edit that the team would make.

The first line is decidable: the plan decides it. The second line is not a
formal property. The engine approximates it with a cost and a ranking. This
is the view-update problem of bidirectional transformations, for a
language (HCL with modules, or a general language) that has no `put`.

### B2. The edit grammar

The engine proposes edits of five kinds only. A small grammar keeps the
search small and each edit easy to read in a review.

| Kind | OpenTofu | Pulumi |
| --- | --- | --- |
| G1. A new block | `resource` + `import`, in the file of its nearest sibling | a declaration with `import`, next to its nearest sibling |
| G2. A new entry in a collection that a loop reads | a new key in the map or set of a `for_each` (in `locals`, a variable default or a tfvars file), or a `count` increase with a list entry | a new item in the literal list or map that a loop reads, or in a stack config value that it reads |
| G3. A new instance of a module or component | a new key of a module `for_each`, or a new `module` block | a new instance of the component class |
| G4. A literal becomes a reference | `var.x`, `local.y`, `module.m.out`, `data.t.n.attr`, another resource | `config.get(...)`, another resource, a stack reference |
| G5. A new input | a new variable and its value in the tfvars file that siblings use | a new config key in the stack settings file |

G4 and G5 combine with G1 to G3.

### B3. Find the candidate sites from provenance

The engine does not guess where similar code is. The tools recorded it.

- **OpenTofu.** Group the managed resources of the same type by their
  configuration address without instance keys. A group with keyed instances
  is a `for_each`; with integer keys, a `count`; under `module.m[...]`, a
  module call with a loop. `tofu show -json` gives the `for_each_expression`
  of the resource or module call, and its references: `local.buckets`,
  `var.buckets`. The engine follows the reference to its definition: a
  `locals` entry (file and line), a variable default, or the tfvars file
  that sets the variable (the unit's variable files are known: ADR 101
  decision 4).
- **Pulumi.** Group the resources of the same type by `sourcePosition`.
  Two or more resources at one position are made by one loop or one
  function. The common prefix of their stack traces gives the function and
  the component. The engine reads the source around that position. When the
  loop reads a literal collection in the same file, or a stack config value,
  the site is a G2. Else the site is not editable by the engine, and only a
  G1 next to it is offered.
- **Across units.** Units of the same namespace whose code locations share a
  repository give more siblings for B6, never edit sites in another unit.

### B4. Invert the template of a loop

For a G2 site, the engine needs the new entry `(k, v)` so that the block
template, evaluated with `each.key = k` and `each.value = v`, gives the
attributes of `r`.

1. **Read the template.** For OpenTofu, the attribute expressions of the
   block, from the configuration JSON and the source. For Pulumi, the
   arguments at the source position.
2. **Anti-unify the siblings.** The evaluated attributes of the existing
   instances, from state, give a template with holes: an attribute equal in
   every instance is a constant of the template; an attribute that differs
   is a hole. If a constant of the template differs from `r`, the site does
   not fit, and the engine stops for this site.
3. **Solve each hole** in a small invertible fragment:
   - `each.value.x`, `each.value["x"]`, `each.key`: direct.
   - A string template of constants and one hole, for example
     `"${var.env}-${each.key}"`: strip the constants (FlashFill-style).
   - `lookup(each.value, "x", d)` and `try(each.value.x, d)`: leave `x` out
     of `v` when `r` has `d`.
   - `merge(local.common, each.value.tags)`: `v.tags` is the tags of `r`
     less the entries equal to `local.common`.
   - A conditional on a field: the field takes the value that selects the
     branch whose result equals `r`.
   - Anything else: the hole is not solvable, and the site does not fit.
4. **Shape of `v`.** `v` has the fields that the siblings' values have, no
   more. A field that the template does not read is left out.
5. **The key `k`.** From a solved string template when there is one, else
   the stable name of A6.
6. **Check.** Write the entry, plan, apply the rule of A7 with the address
   `aws_s3_bucket.this["<k>"]` (or the module path) as the new address.

A module or component makes several resources. A new instance of a module
that makes a bucket, its policy and its versioning plans clean only when all
three exist and are imported together. So G3 works on a **group of
findings**: findings linked by references (A5) or by a shared name prefix.
The engine matches the group to the module by the types and the reference
edges of its resources. A group that does not cover the module's resources
gives a plan that creates the missing ones, so the check fails, and the site
is not offered. This is a heuristic match with an exact check.

### B5. Import in a loop

- **OpenTofu.** An `import` block with `to` set to the new instance address
  imports one item of a `for_each`, a `count` or a module. No change to the
  loop's code is needed.
- **Pulumi.** The `import` option is code. For an item of a loop, the
  engine has two choices:
  - **An import step in the apply.** The change order adds the item, and its
    apply job runs `pulumi import` for the URN that the loop will make,
    before `pulumi up`. The plan job checks against a copy of the state in
    the sandbox, after the same import. Recommended.
  - **A map of import IDs in the loop's options.** Visible in the code and
    left behind after the apply. Not recommended.

### B6. Infer what comes from variables

For G4 and G5 the engine learns from the team's own code how attributes of a
type are fed.

1. **Collect.** For each managed resource of type `T` in the target unit,
   and in the units of the namespace that the person can read, each
   attribute's expression kind from the configuration JSON: constant,
   variable, local, module output, data source, other resource, function.
2. **Ground in values.** For each attribute `x` of `r`, and each expression
   that siblings use for `x`, evaluate that expression in the target unit
   (from state, the plan's `variables` and the module outputs). Keep it only
   when its value equals the value of `r`. Value equality makes every
   substitution exact before the plan checks it.
3. **Prefer the unit, then the namespace.** An expression that the unit's
   own siblings use wins over one from other units. Among equals, the most
   frequent wins.
4. **Tags.** When `r`'s tags contain all of a local map that siblings merge
   (`merge(local.tags, {...})`), write the merge and only the residual tags.
   Drop tags equal to the provider's `default_tags`.
5. **Clusters.** When siblings of `T` split into groups with different
   conventions (Jaccard distance over their set of set attributes and
   expression kinds), take the group nearest to `r`. This is a heuristic.
6. **New inputs (G5).** An attribute that siblings feed from a variable whose
   value differs for `r` becomes a new variable, with its value in the
   tfvars file that siblings use, or a new stack config key.

Pulumi programs have no configuration JSON. B6 for Pulumi uses only value
equality with stack config values, stack outputs and other resources (A5),
and the source text at the sibling's position. It is weaker, and the
evidence says so.

### B7. Rank the candidates

Every candidate is checked by A7 before it is ranked. A candidate that fails
the check is not offered. The engine offers at most 3.

The rank, in order, with ties broken by the order of the list:

1. **Structure.** G2 or G3 into an existing loop or module, then G1 in the
   file of the nearest sibling, then G1 in a new file.
2. **Edit size.** Fewer changed lines and fewer new symbols (Tortoise ranks
   by minimal change).
3. **References.** More literals replaced by references or variables (B6).
4. **Convention.** The name and the tags follow the solved string template
   of the siblings.
5. **Locality.** The directory with the most siblings of the type.

The weights are a heuristic. Slice 0 sets them from the benchmark, and the
evidence of each candidate shows its score parts, so a person can see why one
candidate is first.

### B8. The evidence

Each candidate carries the evidence that a reviewer needs, masked:

- the site and the reason: "`aws_s3_bucket.this` has 14 instances from
  `local.buckets` in `locals.tf:12`; their template differs from this bucket
  only in `bucket` and `tags.Team`";
- the holes and how each was solved;
- each reference and the value that matched (masked when sensitive);
- each heuristic choice, marked as heuristic;
- the plan summary of A7 for each unit planned;
- the values that the team must set (A4).

### B9. Typed edits, the encoders, and the change to ADR 101 decision 4

The runner is not trusted to write code, so it does not. It sends typed
edits. The server checks their content, renders them with its own encoders,
and writes the result.

**The edit schema** (`codify-edit/1`, a JSON document with a strict schema;
unknown fields refused). Each edit is one of:

| Edit | Fields | OpenTofu | Pulumi |
| --- | --- | --- | --- |
| `add-block` | file, kind (`resource`, `module`, `variable`), labels, body | a new block, in a new file or at the end of an existing `.tf` file | - |
| `add-import` | file, target address, `id` or `identity` | an `import` block | - |
| `add-map-entry` | file, the address of the collection (a `locals` entry, a variable default, a tfvars variable), key, value | a new entry in that map | a new entry in a YAML map or a stack config value |
| `add-list-entry` | file, the address of the collection, value | a new element | a new element |
| `add-tfvars-value` | file, variable name, value | a new line in the tfvars file that siblings use | - |
| `add-pulumi-resource` | file, type token, logical name, inputs, options (`import`, `parent`, `provider`, `protect`, `dependsOn`) | - | a declaration in the language of the project |
| `add-pulumi-config` | stack, key, `secret` flag, no value | - | a config key in `Pulumi.yaml` with its type; the value comes from the profile draft (A4) |

A value is a typed tree: `string`, `number`, `bool`, `null`, `list`, `map`,
`object` with schema type names, and `expr`. An `expr` is not text. It is a
closed grammar: a traversal (`var.x`, `local.x`, `module.m.x`,
`<type>.<name>.<attr>`, `each.key`, `each.value.<field>`, `data.<type>.<name>.<attr>`,
`config.<key>` in Pulumi, a resource output in Pulumi) or a call of `merge`
or `lookup` on such terms. No other function, no template, no operator.

**What the server checks, at each edit, without trust in the runner:**

- the schema and the bounds: 1 to 64 edits, 1 to 32 files, 256 KiB of
  values, a depth of 16, at most 1,000 nodes in one value tree;
- the paths: the rules of ADR 101 decision 4 (relative, clean, inside the
  code location, no CI configuration), and only the file kinds of the tool:
  `.tf`, `.tfvars`, `.tfvars.json` for OpenTofu; `Pulumi.yaml`, the stack
  settings file and the source files of the project's language for Pulumi;
- the names: labels, keys and identifiers from the namer's character set
  (A6), never from a raw cloud value;
- each `expr` against the grammar, and each traversal against the symbols
  that the configuration JSON of the job lists (no reference to a symbol
  that does not exist);
- each literal against the secret checks of A4 step 3 and step 6 (the
  digests, the patterns, the entropy);
- each `add-map-entry` and `add-list-entry` against the file: the server
  parses the file at the commit of the job, finds the collection, and
  refuses when it is not a literal map or list in that file;
- no edit removes or changes existing text. Every edit adds.

A failure refuses the whole edit set, with an audit entry, before any review
request exists.

**The encoders.** One escaping encoder for each target: HCL, the tfvars
JSON, Pulumi YAML, TypeScript, Python, Go, C# and Java. Each turns a typed
value into a literal of its language, and nothing else:

- HCL: a quoted string with `\`, `"` and control characters escaped, `${` as
  `$${` and `%{` as `%%{`. Never a heredoc.
- Pulumi YAML: always a double-quoted scalar, `${` as `$${`.
- TypeScript: a double-quoted string literal with escapes, never a template
  literal. Python: a string literal with escapes, never an f-string. Go:
  `strconv.Quote`. C#: a regular string literal, never `$"..."` or `@"..."`.
  Java: an escaped string literal, never a text block.
- All: ` `, ` `, NUL and the bidirectional control characters are
  escaped; non-printable characters are written as escapes.

Each encoder has a test with a hostile corpus: quotes and backslashes,
`${...}`, `%{...}`, `#{...}`, backticks, `"; os.system("...")`,
`"); System.exit(1); ("`, `*/`, newlines, the bidirectional controls, a
string of 1 MiB. The test encodes each string, parses the result with the
parser of the language, and checks that the value is equal and that the
parsed tree is one literal. At run time, the runner and the server parse
each rendered file again before any plan or preview runs, and refuse a file
whose literal count differs from the edits.

**The change to ADR 101 decision 4.** A draft for the ADR that accepts this
RFC:

- Decision 4 keeps its rules for the edits of config and variable files.
- It adds a second kind: **codification edits**, only for the write-back job
  of a `codify` change order. They are typed edits of `codify-edit/1`. The
  server checks and renders them as above. The write-back executor writes
  only the text that the server rendered, never text from a client or a
  runner.
- Every edit adds text. A codification edit never deletes or changes a line.
- The deliver phase waits for the person's confirmation in every mode, as for
  an agent (ADR 125, ADR 128).

**The re-plan is not a security layer.** The no-change check and the plan of
the review request check the result. A hostile runner can report a check
that it did not run. The layers that stop harm are: the typed edits and
their checks in the server, the server's own render, the secret checks in
the server, the person's confirmation, and the plan of the review request
in the sandbox of RFC 122.

## Part C. Dusty

- **No new tool.** `propose_change` (ADR 122) gets the operation `codify`,
  with the finding IDs and the target unit. It starts the `codify` job
  through the job model, under the person's grants: `namespace:triage-unmanaged`
  and `unit:write` (ADR 165), plus the start action of a write-back job.
- **The change order.** The job's chosen candidate goes into a change order
  with a write-back job (ADR 299), as a review request. The deliver phase
  waits for the person's confirmation in every mode (ADR 125, ADR 128).
- **What Dusty does.** It reads the codification result with `get_change`:
  the candidates, the masked evidence and the plan summaries. It may choose
  another of the verified candidates than the first, with a reason. It writes
  the description of the review request from the evidence. It never sees a
  secret value (ADR 124): the evidence holds none.
- **What Dusty does not do in the first version.** It does not write code.
  A later slice may let a model propose an edit for a site that the engine
  cannot solve (an expression outside the fragment of B4, a Pulumi loop over
  computed data). That edit goes into the same `codify` job as one more
  candidate and passes the same check, or it is not offered. This is the
  NSync pattern with our check.
- **The description.** It holds the placement, the evidence, the plan
  summary ("import 1, add 0, change 0, destroy 0; other resources: no
  change"), the values the team must set, and a list of what is heuristic.

## Security

The inputs from outside the process: the finding (from an untrusted sweep
report), the values that the provider read from the cloud, the team's
repository, the placement inputs (state provenance, the configuration JSON,
the siblings in other units), the plan file and plan JSON, the typed edits
and evidence that a runner uploads, and Dusty's choice. The five steps of
ADR 250 for each:

| Input | 1. Check at the boundary | 2. Encode or contain | 3. Limit | 4. Authorize at use | 5. Fail closed |
| --- | --- | --- | --- | --- | --- |
| The finding's type and import ID | The type must be a type of the provider schema; the import ID must match the ID form of the type (allowlist). | Written only through the encoders (B9). | 1 to 50 findings for each job; an ID of at most 1 KiB. | `namespace:triage-unmanaged` and `unit:write` at the start, checked again by the job model. | Unknown type or bad ID: the finding is refused, with an audit entry. |
| The read values | Types from the schema; unknown attributes refused; default-deny (A4 step 1). | **Template and code escape.** A cloud value such as `${file("/etc/passwd")}` or `"); exec("...` must stay one literal. The encoder of each language (B9) escapes it; a parse after the render checks it before any plan or preview. | Size limit for each value and for the edit set (256 KiB). | - | A value that an encoder cannot encode, or that fails the round-trip parse, fails the job. |
| Secret values and unclear values | A4 steps 1 to 3. | Only in sandbox memory, the plan step's environment (masked in the log), and the sealed profile draft. | The digest key lives one job. | The profile attachment of the unit decides which profile secrets the job compares, and who may publish the draft. | The upload guard (A4 step 8) on the runner, and the digest, pattern and entropy checks in the server (A4 step 6, B9). One hit: nothing is written. |
| The plan file and the plan JSON | Parsed strictly; unknown action values are a failure. | Sealed at rest as a job plan of ADR 102, with the `secret` flag; shown only masked. | Size limit. | An unmasked read needs `unit:read-secrets`. | Anything but the pass rule of A7 is a fail. |
| The team's repository | Read only at the job's commit. | Read in the sandbox; the engine runs no code of the repository other than the plan that the job already runs. | Parse limits: file count, file size, nesting depth. | The job's checkout scope. | A parse error drops the site, never the check. |
| Placement: provenance (`sourcePosition`, `stackTrace`, instance addresses) | A source position must name a file inside the code location; a stack frame outside it is ignored. | A position selects a file to read; it is never a path that the engine writes. | At most 10,000 resources and 64 frames for each resource. | The unit's own state, read with the job token. | A position outside the code location drops the site. |
| Placement: the configuration JSON and the collection definition | Strict JSON parse; a collection is a site only when it is a literal map or list in a file of the code location. | The server parses the file again at the commit before it applies an `add-map-entry` (B9). | Parse limits. | - | A collection that is not a literal drops the site. |
| Placement: siblings in other units (B6) | Only units of the same namespace. | Only the expression kinds and the non-sensitive attribute values; never a sensitive value. | At most 200 sibling units. | The person of the job must hold `unit:read` on each sibling unit, checked at the read. | A unit that the person cannot read is skipped, and counted in the evidence. |
| Placement: a shared module or shared path | A path that another unit declares, or a module directory that another unit calls, is a shared path. | - | - | An edit of a shared path needs the owner of that module or path: a separate review request to that owner, or no placement there (question 6). | No owner, or no right: the site is dropped, and the evidence says why. |
| The typed edits, at the server | The schema of B9, unknown fields refused; each `expr` against the grammar and the symbols. | The server renders the text itself with the encoders; the runner's text is never used. | The bounds of B9. | The deliver phase waits for the person; `unit:write-back`. | One failing edit refuses the whole set, with an audit entry, before a review request exists. |
| Dusty's choice | Must name one of the verified candidates of the job. | - | - | The person's grants at the time of the call. | Another value is refused. |

A hostile runner can send typed edits that passed no check, and report a
check that it did not run. The server cannot re-run the plan. The layers that
stop the harm, each on its own: the typed edits and their content checks in
the server, the server's own render, the server's secret checks, the person's
confirmation of the deliver phase, and the plan of the review request in the
sandbox of RFC 122 before any apply. The change order shows that the check
result came from the runner of a named pool.

## Is this a paper?

Possibly. What would be new, against the work in "Research":

1. **Provenance-guided placement.** Use the provenance that IaC tools
   already record (OpenTofu instance addresses and configuration
   expressions, Pulumi source positions and stack traces) to find the loop,
   module or component that a new resource belongs to, in any language, with
   no full static analysis of that language.
2. **Template inversion with an oracle.** Anti-unify the siblings, invert a
   small expression fragment to get a new entry, and accept it only when the
   tool's plan shows a no-op import and a no-op frame. Tortoise uses a
   solver over Puppet; NSync uses an LLM for drift of resources that have
   code. Neither adds an unmanaged resource into an existing loop.
3. **A benchmark that needs no cloud: leave one out.** Take public projects
   with loops and modules and their state. Remove one entry from the code,
   keep the resource in state, and ask the engine to put it back. A plan with
   `-refresh=false` against the copied state checks a candidate with no cloud
   call for OpenTofu. Measure: the share of resources with a verified
   candidate; top-1 and top-3 placement equal to the original (exact, or
   plan-equal); the number of plans; the time. Compare with the floor alone
   (always G1) and with an LLM that proposes edits under the same check.

What is not new: the floor (the tools have it), the plan as the check
(aztfexport and NSync), anti-unification and string synthesis as methods.

Slice 0 builds the benchmark first. Without its numbers, the ceiling is a
proposal, and this RFC says nothing about how often it finds the right site.

## Options

### Option 1. The floor only

The `codify` job of part A. No placement.

- Gives: the action `write-code` of ADR 165, with secrets as references and
  the check, in every language.
- Costs: code that a team must move by hand into its loops.

### Option 2. The floor, then the deterministic ceiling (recommended)

Part A, then part B in slices, then part C.

- Gives: the floor first, then placement where the evidence and the check
  allow it. Every heuristic is visible in the evidence.
- Costs: the placement engine is the largest part. The Pulumi side is weaker
  than the OpenTofu side, because a general language has no configuration
  JSON.

### Option 3. An LLM proposes the edits, the check gates them

Dusty or another model reads the project and writes the edit; the check
accepts or refuses it.

- Gives: reach into code that the fragment of B4 cannot invert.
- Costs: Dusty would need to read source files, which ADR 122 does not
  offer; a model call for each finding; results that change from run to run.
  The published LLM benchmarks show failure rates that make it a poor first
  source. A good later source of extra candidates (slice 8).

### Option 4. Do nothing

The console keeps showing the tools' commands (ADR 165 decision 8). A team
gets `secret("<plain value>")` from `pulumi import` and conflicting arguments
from OpenTofu, and moves the code by hand.

## Proposal

Option 2:

1. Part A for OpenTofu and the six Pulumi languages, with the check as the
   only acceptance test.
2. Write-back of code for a `codify` change order as typed edits that the
   server checks and renders (B9, question 1).
3. Part B, OpenTofu first, Pulumi YAML next, then the other Pulumi
   languages.
4. Part C: Dusty picks and explains verified candidates. A model as a
   source of candidates comes last, under the same check.

### Terms for the glossary, at acceptance

- **Codification**: the action `write-code` on a finding, and its job: code
  for an unmanaged resource, with an import directive, delivered as a
  review request after the no-change check.
- **No-change check**: the plan rule of A7. It is the only acceptance test of
  a codification.
- **Placement**: where a codification puts the code in the code location:
  a new block, a new entry of a loop, a new instance of a module or
  component. Not "placement rule", which is a rule of collections (ADR 61).

## Requests to other designs

- **ADR 100 and ADR 165.** The job operation `codify`, plan-like, on one
  target unit, with finding IDs. ADR 165 already asks for it.
- **ADR 101 decision 4.** A second kind of edit, codification edits: typed
  edits of `codify-edit/1` that the server checks and renders, only for a
  `codify` change order (B9, question 1).
- **ADR 102.** The plan file and the plan JSON of a `codify` job are job
  plans with the `secret` flag.
- **RFC 122.** The plan of a codification review request runs as the plan of
  an untrusted source.
- **ADR 122.** The operation `codify` of `propose_change`. The catalog stays
  at 12 tools.
- **RFC 123.** The `iac` entry of sweep report version 2 is the input of the
  floor. Without it, the runner keeps a mapping table.
- **RFC 5 and ADRs 200 to 209 (profiles).** The runner compares read secret
  values with profile secrets by keyed hash, without uploading either.

## One-way doors

Before 1.0 the tool-neutral API can change (ADR 73). The doors below are in
the team's repositories or in a trust boundary, so they stay.

- **Code in the team's repositories.** Once a team merges generated code,
  its names, its variable names and its secret references are in their code
  and their state. The name function (A6) and the secret reference form (A4)
  must be fixed before the first merge, and keep a version.
- **No markers in the team's code.** This RFC proposes no comment or tag in
  the generated code that names OpenDustpan or a finding. The link lives in
  the change order. Adding markers later is possible; removing them from
  merged code is not.
- **Write-back of code, and the edit schema.** Once the server writes source
  files to a forge, the typed edit schema and its checks are a security
  boundary that teams depend on. A free-text patch later would remove the
  content checks.
- **The non-secret list (A4).** Each entry lets a value of one attribute
  into Git. An entry that let a secret through cannot be taken back from
  merged code; the secret must be rotated.
- **The check as the gate.** If a not-verified codification is ever offered
  for write-back, the guarantee is gone for every team that trusted it.
- **The MPL-2.0 HCL library in the runner and the server.** Once the
  edits depend on it, a replacement is a rewrite.

Two-way doors: the ranking weights, the fragment of B4, the number of
candidates, the mapping table, the languages order.

## Slices

Each slice ends with a test that names its pass fact.

| Slice | Content | Pass |
| --- | --- | --- |
| 0. Benchmark | The leave-one-out harness and corpus (public projects with loops and modules, their state). The floor of the tools measured: the share of generated resources that plan clean. No product code. | A report with the numbers, in `docs/research/`. |
| 1. Floor, OpenTofu | `codify` job, A2 to A7, the typed edits and the encoders with their hostile corpus, the server checks of B9, the result as an artifact, the console shows the rendered diff. No write-back. | e2e: a bucket in no unit; its edits plan "import 1, change 0", other resources no-op; a tag value with `${...}` stays a string; a Lambda environment variable becomes a variable whose value is in a profile draft; the server refuses an edit set with a literal equal to a profile secret. |
| 2. Floor, Pulumi | A8 for YAML, TypeScript and Python, then Go, C# and Java. | e2e for each language: the same pass rule with `pulumi preview`; no `secret("` literal in any file. |
| 3. Write-back of code | ADR 101 decision 4 changed; change order with the typed edits; the plan of the review request runs the check again in the sandbox of RFC 122. | e2e: the review request opens; its plan passes; after the merge and the apply, the finding resolves. |
| 4. Ceiling, OpenTofu G2 and G3 | B3, B4, B5, B7, B8. | Benchmark: a stated share of resources gets a verified G2 or G3 candidate. e2e: a new bucket becomes a new key of the existing `for_each` map. |
| 5. Ceiling, references and inputs | B6, G4 and G5. | e2e: `vpc_id` becomes the module output that siblings use. |
| 6. Ceiling, Pulumi | B3 by source position, G2 for YAML and literal collections, the import step of B5. | e2e: a new item in a YAML loop list, with the import step. |
| 7. Dusty | Part C: `propose_change` with `codify`, the description from the evidence. | Agents milestone exit test 2 with a codification change order. |
| 8. Model candidates | A model proposes an edit for an unsolved site; the same check. | Benchmark: the share of extra verified candidates, and the cost. |

## Where it fits in the roadmap

A proposal. This pull request adds a row under "Later" only, as the roadmap
rules ask.

- **Slices 0 to 6** belong with Sweep: in the sweep plugin milestone that
  RFC 123 proposes (its name there is "Sweep plugins"), as a second part, or
  in a milestone right after it. They need runners (milestone 5), the job
  operation of ADR 165, the `iac` entry of RFC 123, and for slice 3 the
  write-back executor.
- **Slices 7 and 8** belong in the agents milestone (milestone 10 in the
  roadmap today): they need Dusty runs and the change orders of agents.

The placement in milestones and Dusty's role (part C: choose and explain
first, model-written candidates last) are proposals that follow the roadmap
rules and ADRs 125 and 128. They are not among the questions below. The
owner can change them in the review of this RFC.

## Open questions for the owner

1. **The form of code write-back.** How does the server write code for a
   codification change order?
   - A. Typed edits of `codify-edit/1`. The server checks their content and
     renders the text with its own encoders (B9). **Recommended.**
   - B. A text patch from the runner, with path checks only.
   - C. No write-back. The console shows the diff, and a person copies it.

   Recommendation: A. B lets a runner put any text in the team's repository.
   C leaves the work to the person.
   _ELI5: A: the helper sends a list of "add this toy to shelf 3", and we
   put it there ourselves. B: the helper rearranges the shelf, and we only
   check that it stayed in the right room. C: we show you a photo, and you
   do it._
2. **Who writes the first draft.** How does the runner get the first draft of
   a resource?
   - A. Run the team's own `tofu` and `pulumi` as processes
     (`-generate-config-out`, `pulumi import --preview-only`), then the
     minimizer and the typed edits. **Recommended.**
   - B. Our own generator from the provider schema and the read values.
   - C. A and B: our generator only where the tool's draft fails.

   Recommendation: A. The tools already read through the provider with the
   team's versions. B is more code and must follow every provider change.
   _ELI5: A: the toy makers have a drawing machine, and we use it. B: we
   build our own machine. C: we use theirs, and ours when theirs jams._
3. **The HCL library.** May OpenDustpan link `github.com/hashicorp/hcl/v2`
   (MPL-2.0)?
   - A. In the runner and in the server. The server needs it to check and
     apply an `add-map-entry` into an existing HCL file. **Recommended.**
   - B. In the runner only. The server applies only edits that need no HCL
     parse: new `.tf` files and `.tfvars.json` entries. A new entry of a
     `for_each` map in `locals` is then not possible.
   - C. Neither. We write a parser of the subset of HCL that we edit.

   Recommendation: A. B removes the main case of the ceiling. C is a parser
   that must match HCL exactly, which is a security risk of its own.
   _ELI5: we need one pair of scissors to cut paper of this shape. A: the
   helper and we both get scissors. B: only the helper, so we can only add
   whole new pages. C: we make our own scissors._
4. **Values that may be secrets.** Which values may go into Git as literals?
   - A. Default-deny: only numbers, booleans, references and the entries of
     a reviewed non-secret list with a shape; every other value goes to a
     sealed profile draft that a person publishes (A4). **Recommended.**
   - B. Only the values that the provider marks sensitive, and the names
     that the credential words match, become references.
   - C. Only the values that the provider marks sensitive.

   Recommendation: A. B and C let a password in a Lambda environment
   variable or in user data into Git.
   _ELI5: A: nothing goes on the label unless we know it is safe; everything
   else goes into the locked drawer. B: we lock away what looks like a key.
   C: we lock away only what the toy maker called a key._
5. **Protection of an imported resource.** Does a codified resource get
   protection from a delete?
   - A. Follow the siblings (B6): `prevent_destroy` or `protect` when the
     team's resources of that type have it; else protection on.
     **Recommended.**
   - B. Always on: Pulumi `protect: true` (the default of `pulumi import`)
     and OpenTofu `prevent_destroy = true`.
   - C. Never.

   Recommendation: A. Nobody planned the life of a resource that was
   unmanaged, so a delete by mistake must not be easy; but a team whose
   resources have no protection would remove it from every codification.
   _ELI5: a toy we found goes back in the box with a "do not throw away"
   sticker, unless the other toys in that box never have one._
6. **Shared modules and shared paths.** May a codification edit a module
   directory or a path that another unit uses?
   - A. No, in the first slices. Only the unit's own code location.
     **Recommended for now.**
   - B. Yes, with the owner of that module or path: a separate review request
     to that owner, and the no-change check on every unit that declares the
     path.
   - C. Never. A resource that fits only a shared module gets a new block in
     the unit.

   Recommendation: A now, then B in a later slice. C loses the cases where
   the module is the right place.
   _ELI5: first we only tidy your own room. Later we may put a toy on the
   shared shelf, but only when the person who owns that shelf says yes._

## Checklist

- [x] Tolerates asynchronous replication between installations: each
  namespace writes only its own half, under its own fence, and no write waits
  for another installation (ADR 248 decisions 3, 6 and 9). A codification is
  a job of one unit and a change order of one namespace. Its result, its
  evidence and the finding's name are namespace data under the fence of that
  namespace. B6 reads units of the same namespace only.

## Outcome

Empty until the RFC is accepted (link the ADR) or dropped (say why).
