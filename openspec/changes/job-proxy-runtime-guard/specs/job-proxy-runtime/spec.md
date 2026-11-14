## ADDED Requirements

### Requirement: No proxy without rules

When a unit has no proxy rules, the server SHALL run its jobs with no job proxy.

#### Scenario: Unit without proxy rules
- **WHEN** a job runs on a unit with no proxy rules
- **THEN** no job proxy starts and sandbox routing stays unchanged.

### Requirement: One proxy as only exit

While a job of a unit with proxy rules runs, the server SHALL route all sandbox egress through one job proxy next to that job.

#### Scenario: Direct egress blocked
- **WHEN** a step attempts a TCP connection that does not go through the job proxy address
- **THEN** the connection fails and the phase ends failed.

### Requirement: Host proxy placement

While a firecracker or gvisor claim with proxy rules runs, the server SHALL start one job proxy on the host with one sandbox route to the job proxy address.

#### Scenario: Sandbox with dropped packets
- **WHEN** a claim with proxy rules runs with firecracker or gvisor isolation
- **THEN** only packets to the job proxy address pass and DNS included other packets drop.

### Requirement: Kubernetes and process placement

While a kubernetes or process claim with proxy rules runs, the server SHALL run one job proxy per claim as the only exit of its steps.

#### Scenario: Pod or process egress
- **WHEN** a claim with proxy rules runs with kubernetes or process isolation
- **THEN** all step egress passes the job proxy of the claim and direct egress fails.

### Requirement: Proxy rule load

When the coordinator starts the job proxy, the server SHALL load the joined proxy rule sets of the unit from the claim assignment.

#### Scenario: Rules at claim start
- **WHEN** the coordinator starts the job proxy
- **THEN** the proxy refuses hosts outside that joined set.

#### Scenario: Stale proxy
- **WHEN** the coordinator cannot deliver the joined proxy rule sets
- **THEN** the claim fails before steps start.

### Requirement: Server reach through tunnel

While a proxied phase runs, the server SHALL allow launcher and tool traffic to the server host only through a built-in tunnel rule.

#### Scenario: Launcher reaches server
- **WHEN** the launcher polls the server during a proxied phase
- **THEN** the launcher reaches the server host through the tunnel and keeps its own TLS check.

### Requirement: Proxy start failure

If the job proxy fails to start, then the server SHALL fail the claim before any step starts.

#### Scenario: Proxy fails to start
- **WHEN** the coordinator cannot start the job proxy
- **THEN** the claim ends failed before any step starts.

### Requirement: Proxy loss mid-phase

If the job proxy stops during a phase, then the server SHALL fail pending and new requests closed with no direct fallback.

#### Scenario: Proxy stops
- **WHEN** the job proxy stops during a phase
- **THEN** in-flight requests fail closed and the phase ends failed.

### Requirement: Launcher tunnel loss

If the job proxy stops while the launcher polls through the tunnel, then the server SHALL fail the poll closed.

#### Scenario: Poll during proxy loss
- **WHEN** the job proxy stops while the launcher polls the server
- **THEN** the poll fails closed and the phase ends failed with no direct server dial from steps.

### Requirement: Unnamed host refused

When a job requests a host that no proxy rule names, the server SHALL refuse the request before any upstream connection.

#### Scenario: Unknown host
- **WHEN** a job sends CONNECT for a host that no proxy rule names
- **THEN** the proxy answers 403 before any TLS and writes the blocked line with host and path.

### Requirement: Request identity check

When the SNI or Host header differs from the CONNECT host, the server SHALL refuse the connection.

#### Scenario: Mismatched identity
- **WHEN** the SNI and the Host header of an inspect request differ from the CONNECT host
- **THEN** the proxy refuses the connection without an upstream dial.

### Requirement: Non-canonical path

When a request carries a path that is not canonical, the server SHALL refuse the request without normalization.

#### Scenario: Dot segments
- **WHEN** a request carries a path that is not canonical
- **THEN** the proxy refuses the request and logs the host with a fixed reason and no raw path.

### Requirement: Denied address refused

When a tunnel host resolves to an address that the outbound guard denies, the server SHALL refuse the connection to that address.

#### Scenario: Resolved denied address
- **WHEN** a named tunnel host resolves to an address that the outbound guard denies
- **THEN** the proxy refuses the request and writes the blocked line with host and path.

### Requirement: Bound dial

When the job proxy dials a tunnel host, the server SHALL connect only to a resolved address that it checked with the outbound guard.

#### Scenario: Checked address dialed
- **WHEN** a tunnel request passes the proxy rule and guard checks
- **THEN** the proxy connects only to an address that it checked.

#### Scenario: Mixed addresses
- **WHEN** a host resolves to one allowed and one denied address
- **THEN** the proxy uses only an allowed checked address and never dials a denied or unchecked address.

### Requirement: Upstream proxy carries connection

Where a pool names an upstream proxy, the server SHALL still apply the outbound guard to the resolved address of each rule host.

#### Scenario: Guard still applies
- **WHEN** a pool names an upstream proxy for a proxied phase
- **THEN** the proxy checks the resolved rule host with the outbound guard and opens the upstream connection only on allow.

### Requirement: Blocked line in job log

When the job proxy refuses a request, the server SHALL write blocked by the job proxy with the host and the path to the job log.

#### Scenario: Blocked request logged
- **WHEN** the job proxy refuses a request
- **THEN** the job log holds blocked by the job proxy with the host and the canonical path, a fixed reason, and no header, body, query value, or credential.

#### Scenario: Hostile log value
- **WHEN** a refused request carries line breaks or a non-canonical path
- **THEN** the log holds one line with stripped line breaks and no raw non-canonical path.
