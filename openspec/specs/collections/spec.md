# collections Specification

## Purpose
The collections capability covers collections, named sets of units in one namespace, and the rules that name a collection. A rule refers to a collection by its stable ID, and the name is a label only.

## Requirements

### Requirement: A placement rule names its collection by the stable ID

The server SHALL store and take the collection of a placement rule by its stable ID, and SHALL answer the current name only as a label.

#### Scenario: A collection made again under the same name
- **WHEN** a collection is removed and a new one is made with the same name
- **THEN** the placement rules of the old collection place no unit into the new one

#### Scenario: A rule body names a collection by name
- **WHEN** a client creates a placement rule with `collection` and no `collectionID`
- **THEN** the server answers 400 placement.collection-id-required and stores nothing
