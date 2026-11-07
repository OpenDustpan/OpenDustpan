# 316. Approval of the provider dependencies and the OpenPGP library

Status: accepted · 2026-11-01. Records decisions of the project owner of 18 and 20 October 2026

Changes:

- Extends ADR 176: the dependencies of the provider kit.
- Extends ADR 177: the OpenPGP library.

Changed by:

- ADR 349 changes decision 1: the OpenPGP library is also allowed in the server registry.
- ADR 349 extends decision 2: the approved versions apply in the server registry.

## Context

The project owner made these decisions in the owner decision files of 18 and 20 October 2026. A check on 1 November 2026 found no ADR that states them. ADR 176 lists the dependencies as open.

This ADR writes the words of the owner. It adds no decision of its own.

## Decision

1. **Dependencies of the provider kit (owner, 2026-10-18).** The verbatim quote of the owner:

   ```text
   (18 October) Provider dependencies (gRPC, protobuf, tfplugin6.proto and go-plugin .proto files under MPL-2.0, a YAML library, OpenPGP signing) are approved for opendustpan/providerkit only, not for the server.
   ```

2. **The OpenPGP library (owner, 2026-10-20).** The verbatim quote of the owner:

   ```text
   OpenPGP library APPROVED (owner, 2026-10-20, after provenance check): github.com/ProtonMail/go-crypto v1.5.2, github.com/cloudflare/circl v1.6.3, golang.org/x/crypto >= v0.56.0 (RFC 101 Q6, #1221).
   ```

## Consequences

- ADR 176 and ADR 177 get a pointer to this ADR.
