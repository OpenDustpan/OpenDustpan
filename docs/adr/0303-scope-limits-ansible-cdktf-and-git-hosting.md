# 303. Scope limits: no Ansible stack, no CDKTF, and not a Git host

Status: accepted · 2026-11-01. Decided by the project owner on 1 November 2026

Changes:

- Extends ADR 271 decision 11: decision 3 of this ADR uses the words "Git host" for the service, as that decision requires.

## Context

The project owner made three statements on 1 November 2026. Each statement
limits what OpenDustpan supports. Each statement needs a record, so that a
reader does not have to ask the owner again.

The first statement covers Ansible. The second covers the Cloud Development
Kit for Terraform (CDKTF). The third covers the Git host, which the owner
distinguishes from the review of infrastructure changes.

The owner states that CDKTF was officially deprecated and sunset on
10 December 2025. The owner states that its vendor no longer maintains or
updates it.

## Decision

1. **OpenDustpan has no Ansible stack type and no Ansible runner.** AWX and the AWX provider cover Ansible
   work. An apply can start an AWX job
   template as a follow-up. The follow-ups after an apply are an open question
   in [RFC 96](../rfc/0096-follow-ups-after-an-apply.md). This ADR does not
   decide RFC 96. A later ADR that accepts RFC 96 decides the details.

2. **CDKTF is out of scope.** OpenDustpan adds no CDKTF support. No stack type,
   adapter, runner or endpoint enters the code for CDKTF.

3. **OpenDustpan is not a Git host.** It does not host Git repositories, issues
   or pull requests. It is not a general CI service for application code.
   - It reviews infrastructure changes. A review includes the source diff of a
     change next to its plan, in a change order or in a review request.
   - Its runners run infrastructure jobs. These include CI-like tasks for units
     with no code, which a person starts through self-service.
   - It connects to Git hosts through the Forge port (ADR 271). The port reads
     and writes the repositories, pull requests and statuses in the Git host.
     The Git host keeps the repositories, the issues and the pull requests.

## Consequences

- A request to add an Ansible stack type, an Ansible runner, CDKTF support or
  Git repository hosting is out of scope under this ADR.
- The AWX follow-up may use the AWX job template as the target, as RFC 96
  decides. It does not add an Ansible engine to OpenDustpan.
- Documentation and console text say "Git host" for the service, as ADR 271
  decision 11 requires. The text does not say that OpenDustpan hosts code.

## Not decided here

- The details of the AWX follow-up, including its credential and its record
  of the job. RFC 96 holds these questions.
- Support for a tool that is not named in this ADR. ADR 12 and its later
  ADRs decide new tools.
