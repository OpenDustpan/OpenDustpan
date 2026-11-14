## ADDED Requirements

### Requirement: Host literal guard

When a source URL holds an IP literal or a private, loopback, link-local or unique-local host, the server SHALL refuse the fetch.

#### Scenario: IP literal refused
- **WHEN** a source URL holds an IP literal
- **THEN** the fetch fails without network use.

### Requirement: Pinned fetcher

When a source fetch runs, the server SHALL resolve the host once, dial that pinned IP through the shared dial guard and follow no cross-host redirect.

#### Scenario: Rebinding refused
- **WHEN** a fetch meets a cross-host redirect or a rebinding answer
- **THEN** the fetch fails and sends no credential to another host.

### Requirement: Checkout guard

When a source checkout extracts an archive, the server SHALL refuse each entry that escapes the checkout directory.

#### Scenario: Zip-slip refused
- **WHEN** an archive holds .., an absolute path, .git, NUL or an escaping symlink
- **THEN** the checkout stops with an error and writes no file outside the directory.
