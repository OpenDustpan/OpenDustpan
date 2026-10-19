---
rfc: 82
title: A namespace in the scope key of a blueprint version
status: discussing
created: 2026-10-19
---

# RFC 82. A namespace in the scope key of a blueprint version

Status: discussing.

## Problem

ADR 224 decision 1 gives a blueprint version the scope key
`blueprint/<blueprint>/<version>`. A blueprint is an object of a
namespace: the Catalog stores it by namespace and name
(`catalog.BlueprintRef` holds both). Two namespaces can each publish a
blueprint `web`. So the key `blueprint/web/1` can name two blueprint
versions.

The key is a one-way door. Stored policy IDs, decision records and
requirement names hold it. A policy ID such as `blueprint/web/1/validate`
in a record does not say which namespace the gate came from. A change of
the key after the first tag needs a data conversion of each stored key.

`pkg/gate` follows ADR 224 decision 1 today. `gate.BlueprintScope` takes
the blueprint name and the version number, and the key holds no
namespace.

## Options

- **A. Do nothing.** Keep `blueprint/<blueprint>/<version>`. A reader of a
  record finds the namespace from the namespace of the record. This works
  only while a change uses blueprints of its own namespace. A blueprint
  that another namespace shares makes the key point at the wrong object.
- **B. Add the namespace.** Use
  `blueprint/<namespace>/<blueprint>/<version>`, the same order as
  `profile/<namespace>/<profile>` and `collection/<namespace>/<collection>`.
  Each key names one blueprint version. The cost is one more segment, and
  an edit of ADR 224 decision 1 through a new ADR.
- **C. Use the stable blueprint ID.** Use `blueprint/<blueprint ID>/<version>`.
  A rename keeps the key. A person cannot read the key, and the record
  needs a lookup to show a name. No other scope key holds an ID.

## Proposal

Option B. It makes each key name one object, it follows the shape of the
other keys of a namespace object, and no tag holds a blueprint scope key
yet, so the change needs no data conversion. `Scope.Namespace` then gives
the namespace of a blueprint scope, as it does for a profile scope.

## Open questions

- Does a blueprint of one namespace ever hold gates that a change of
  another namespace meets? If yes, the record must name both namespaces.
- Does option B change the routes of ADR 230 decision 3? A gate of a
  blueprint version has no route of its own, so the expected answer is no.

## Outcome

Empty until the project owner accepts or drops the RFC.
