# 51. Collections: a named set of units in a namespace

Status: accepted · 2026-10-13

## Context

[RFC 4](../rfc/0004-what-sits-under-a-namespace.md) asks what sits under a
namespace. A namespace is the unit of tenancy, home region and fencing
(ADR 4). On 2026-10-11 the project owner asked for a grouping of units that
teams manage themselves. Teams want to use that grouping for access rules,
for the profiles of RFC 5 and for the output sharing of RFC 15.

On 2026-10-12 the owner chose the word: "I guess collection sounds good."
The owner said that a unit is in no more than one collection ("No."). The
owner also said that groupings do not nest.

On 2026-10-12 the owner answered "Yes." to the question "Can a role be
granted on a label?" On 2026-10-13 the owner replaced that answer with
"Collections only". A grant goes on a namespace, a collection or a unit.
Labels are for views and filters, and they never grant access.

## Decision

1. **A collection is a named set of units in one namespace.** The units can
   come from any tool. A team can make and change its own collections, if
   the authorization model allows it (ADR 52). An operator does not have to
   make them.
2. **A collection is soft.** It has no home region and no fence of its own.
   Its writes carry the fence of its namespace (ADR 4). A collection is
   never a part of a unit name. A unit keeps its own name and its own URL in
   its tool.
3. **Collections do not nest.** A collection holds units, not collections.
4. **A unit is in no more than one collection.**
5. **Membership is explicit.** A person or a client adds a unit to a
   collection, or removes it, with an API call. Each change has a declared
   action, passes the policy check and writes an audit entry (ADR 17). A
   label, a tag or a name pattern never puts a unit into a collection.
6. **Labels never grant access.** A label (a tag that a tool sends, or a
   label of OpenDustpan) is for views and filters. A saved view can look
   like a collection, but it grants nothing. This replaces the owner's
   2026-10-12 "Yes." to grants on a label.
7. **A collection is an object of a grant.** A grant, a profile attachment
   (RFC 5) and an output-sharing rule (RFC 15) can name a namespace, a
   collection or a unit as their object. The actions stay the same. The
   collection only widens the object (ADR 13, ADR 17).
8. **A blueprint can choose the collection.** A blueprint (RFC 14) can name
   the collection of the unit that it makes. It can also stop a later change
   of that collection. Both are options of the blueprint.
9. **No mapping from Kubernetes namespaces.** The Kubernetes operator
   (RFC 31) does not map a Kubernetes namespace to a collection.
10. **API.** Each collection operation has a REST API and an action
    (ADR 17).

## Not decided here

[RFC 4](../rfc/0004-what-sits-under-a-namespace.md) keeps these open:

- Must each unit be in a collection, or can a unit be in no collection?
- The name rules of a collection, and whether the TFE-compatible API shows a
  collection as a TFE project.
- How a new Pulumi stack gets its collection. The RFC sketches placement
  rules on the Pulumi project and the stack name.
- Which rights a person needs to add a unit to a collection, to remove it,
  and to move it to another collection.
- The other first ideas of RFC 4: the first screen of a namespace, the
  environment as a concept, views across namespaces and the tags that the
  inventory indexes.

## Consequences

- The `Authorizer` takes a collection as an object, beside a namespace and a
  unit (ADR 17).
- The inventory (ADR 13) can show the collection of each unit. The tool
  packages keep their own models. The collection is OpenDustpan data beside
  the unit, not a field of a tool model.
- A collection needs numbered migrations in each SQL dialect (CLAUDE.md).
- `GLOSSARY.md` gets the term "collection".
