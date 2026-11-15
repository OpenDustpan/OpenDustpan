## ADDED Requirements

### Requirement: One Fetcher for each cache directory in a process

The e2e suite SHALL use one CLI Fetcher for each cache directory in a process, so that no test checks a cached CLI a second time and deletes it while another test runs it.

#### Scenario: Two tests use the shared cache
- **WHEN** two tests of one process install the same CLI from the shared cache directory
- **THEN** they get the same Fetcher, the entry is checked once, and no test deletes it
