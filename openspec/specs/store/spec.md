# store Specification

## Purpose
This capability covers the behavior of the stores that keep the data of an installation. It states the rules for the settings of the SQLite write-ahead log guard of one store. It does not cover the schema, the queries or the PostgreSQL store.

## Requirements

### Requirement: The WAL guard settings belong to one store

The SQLite store SHALL hold the settings of its write-ahead log guard as its own fields, so that a test that tunes the guard of one store changes no other store.

#### Scenario: Two stores in one test binary
- **WHEN** a test opens a store with a low guard threshold and another test opens a store with the default threshold
- **THEN** the second store keeps the default threshold and logs no tick of the first store
