## ADDED Requirements

### Requirement: Exclude refused namespaces from every-object lists

When All is set and a namespace allowlist refuses the client address, the server SHALL exclude that namespace from list results.

#### Scenario: Admin outside the allowlist lists namespaces
- **WHEN** A principal with All set lists namespaces from an address that the namespace allowlist refuses.
- **THEN** The refused namespace is absent from the list while other namespaces remain.

#### Scenario: Token caller outside the allowlist lists namespaces
- **WHEN** A caller with an access token with All set lists namespaces from a refused address.
- **THEN** The refused namespace is absent from the list.

#### Scenario: Collection and unit lists hide refused namespaces
- **WHEN** A principal with All set lists collections or units from a refused address.
- **THEN** No collection or unit of the refused namespace appears in the list.

#### Scenario: Deleted-units list hides refused namespaces
- **WHEN** A principal with All set lists deleted units from a refused address.
- **THEN** No deleted unit of the refused namespace appears in the list.

#### Scenario: Paged list hides refused namespaces
- **WHEN** A principal with All set lists namespaces, collections, units, or deleted units by page from a refused address.
- **THEN** No page and no total holds a row of the refused namespace.

### Requirement: No exclusion when the feature is off

When the installation IP allowlist feature is off or the scope did not opt in, the server SHALL exclude no namespace.

#### Scenario: Feature off or scope not opted in
- **WHEN** The installation feature is off or the scope did not opt in to an allowlist.
- **THEN** The list returns every granted namespace with no exclusion.

### Requirement: Show exclusions in caller access

When All is set with excluded namespaces, the server SHALL show the excluded namespaces next to all in caller access.

#### Scenario: Caller checks access from outside the allowlist
- **WHEN** A principal with All set reads caller access from a refused address.
- **THEN** The answer names the refused namespace next to all.

#### Scenario: Caller checks access with no refusal
- **WHEN** A principal with All set reads caller access from an address that no allowlist refuses.
- **THEN** The answer shows all with an empty exclusion set.

#### Scenario: Caller without All checks access from a refused address
- **WHEN** A principal without All set reads caller access from a refused address.
- **THEN** The answer shows only the granted objects and no every-object flag.

### Requirement: Bound the namespace-with-list read

When the count of namespaces with an allowlist passes 1000, the server SHALL refuse the gate read instead of loading every name.

#### Scenario: Gate reads at the limit
- **WHEN** The count of namespaces with a list is at 1000.
- **THEN** The gate fills exclusions for each refused namespace.

#### Scenario: Gate reads past the limit
- **WHEN** The count of namespaces with a list is past 1000.
- **THEN** Every list call with All set fails with a limit error until the count drops, while a direct read of one named namespace still answers 403.

### Requirement: Use the real client address

When the gate checks an allowlist, the server SHALL use the client address from the trusted-proxy rule.

#### Scenario: Spoofed header from untrusted peer
- **WHEN** A request carries a forwarded header from an address that is not a named proxy.
- **THEN** The gate uses the TCP peer address and still excludes the refused namespace.

### Requirement: Compute exclusions per request

When a principal lists objects, the server SHALL compute exclusions from the allowlists read for that request.

#### Scenario: Allowlist changes between lists
- **WHEN** An administrator changes a namespace allowlist between two list calls.
- **THEN** The second list reflects the changed allowlist.

### Requirement: Fail closed on gate read error

When the gate cannot read the namespaces with a list, the server SHALL refuse the list read.

#### Scenario: Store read fails
- **WHEN** The namespaces-with-list read returns an error.
- **THEN** The list call fails instead of showing refused namespaces.

### Requirement: Empty exclusion set lists all

When no namespace is excluded, the server SHALL return the full list with no filter clause.

#### Scenario: No refused namespace
- **WHEN** No allowlist refuses the client address.
- **THEN** The list returns every granted namespace.

### Requirement: Direct read of refused object

When a caller reads one refused namespace or its objects by name, the server SHALL answer 403 with the allowlist reason.

#### Scenario: Grant holder reads refused namespace
- **WHEN** A caller that holds a grant on a namespace reads that namespace from a refused address.
- **THEN** The server answers 403 with the allowlist reason.

### Requirement: Installation-scoped objects stay visible

When a namespace exclusion applies, the server SHALL keep the visibility of installation-scoped objects unchanged.

#### Scenario: Refused client lists installation objects
- **WHEN** A principal with All set lists installation-scoped objects from an address that a namespace allowlist refuses.
- **THEN** The list still shows the granted installation-scoped objects.
