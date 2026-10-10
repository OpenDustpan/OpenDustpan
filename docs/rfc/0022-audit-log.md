---
rfc: 22
title: Audit log
status: draft
created: 2026-10-10
---

# RFC 22. Audit log

Status: draft. A stub opened from the competitor review in the research doc
"IaC and TACOS landscape". It records the gap and first ideas only.

## Problem

Every competitor with a console has an audit log, usually in a paid tier:
Spacelift (Enterprise), HCP Terraform (Standard), Pulumi Cloud (Pro, export on
Enterprise), Terramate (Enterprise), Stategraph (paid). OpenDustpan's RFCs
record some events (profile opens in RFC 5, policy decisions in RFC 13, egress
requests in RFC 16) but have no common log.

## First ideas

- One append-only log for every action that changes something or reads a
  secret: who, what, which unit or object, when, from where, and the result.
- Query and filter in the API and the console, export (JSON, CEF), streaming
  to an external sink, retention.
- Entries keep their context when the object is deleted.
- RFCs 5, 13, 16 and others write into it instead of keeping their own logs.

## Open questions

- Is the audit log a port with adapters (database, file, syslog, object
  storage)?
- Which events are always on, and which can an operator turn off?
- How does the log stay tamper-evident, for example with a hash chain?

## Outcome

None yet.
