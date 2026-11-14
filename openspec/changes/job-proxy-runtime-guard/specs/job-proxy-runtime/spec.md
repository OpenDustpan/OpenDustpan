## ADDED Requirements

### Requirement: No proxy without rules

When a unit has no proxy rules, the job proxy SHALL not start for its jobs.

#### Scenario: Unit without proxy rules
- **WHEN** a job runs on a unit with no proxy rules
- **THEN** no job proxy starts and sandbox routing stays unchanged.

### Requirement: One proxy as only exit

While a job of a unit with proxy rules runs, the job proxy SHALL serve as the only egress of its sandbox.

#### Scenario: Direct egress blocked
- **WHEN** a step attempts a TCP connection that does not go through the job proxy address
- **THEN** the connection fails with no direct path around the proxy.

### Requirement: Host proxy placement

While a firecracker or gvisor claim with proxy rules runs, the coordinator SHALL start one job proxy on the host with one sandbox route to the job proxy address.

#### Scenario: Sandbox with dropped packets
- **WHEN** a claim with proxy rules runs with firecracker or gvisor isolation
- **THEN** only packets to the job proxy address pass and other packets including DNS drop.

### Requirement: Kubernetes and process placement

While a kubernetes or process claim with proxy rules runs, the coordinator SHALL run one job proxy per claim as the only exit of its steps.

#### Scenario: Pod or process egress
- **WHEN** a claim with proxy rules runs with kubernetes or process isolation
- **THEN** all step egress passes the job proxy of the claim and direct egress fails.

### Requirement: Route stays on restart

While a proxied phase runs, the coordinator SHALL keep the sandbox route drop in place across proxy stop and restart.

#### Scenario: Proxy restarts or stops
- **WHEN** the job proxy of a claim stops or restarts mid-phase
- **THEN** steps keep no direct route and new connections fail closed.

### Requirement: Cross-claim isolation

While two proxied claims share a host, the job proxy SHALL refuse a connection from outside its own claim sandbox.

#### Scenario: Neighbor claim probe
- **WHEN** a step of one claim attempts the proxy address of another claim on a shared host
- **THEN** the connection fails.

### Requirement: Proxy rule load

When the coordinator starts the job proxy, the coordinator SHALL load the joined proxy rule sets of the unit from the claim assignment.

#### Scenario: Rules at claim start
- **WHEN** the coordinator starts the job proxy
- **THEN** the proxy refuses hosts outside that joined set.

#### Scenario: Stale proxy
- **WHEN** the coordinator cannot deliver the joined proxy rule sets
- **THEN** the claim fails before steps start.

### Requirement: Server reach through tunnel

While a proxied phase runs, the job proxy SHALL allow launcher and tool traffic to the server host only through a built-in tunnel rule.

#### Scenario: Launcher reaches server
- **WHEN** the launcher polls the server during a proxied phase
- **THEN** the launcher reaches the server host through the tunnel and keeps its own TLS check.

### Requirement: Proxy start failure

If the job proxy fails to start, then the coordinator SHALL fail the claim before any step starts.

#### Scenario: Proxy fails to start
- **WHEN** the coordinator cannot start the job proxy
- **THEN** the claim ends failed before any step starts.

### Requirement: Proxy loss mid-phase

If the job proxy stops during a phase, then the job proxy SHALL fail pending and new requests closed with no direct fallback.

#### Scenario: Proxy stops
- **WHEN** the job proxy stops during a phase
- **THEN** in-flight requests fail closed and the phase ends failed.

### Requirement: Launcher tunnel loss

If the job proxy stops while the launcher polls through the tunnel, then the launcher SHALL fail the poll closed.

#### Scenario: Poll during proxy loss
- **WHEN** the job proxy stops while the launcher polls the server
- **THEN** the poll fails closed and the phase ends failed with no direct server dial from steps.

### Requirement: Unnamed host refused

When a job requests a host that no proxy rule names, the job proxy SHALL refuse the request before any upstream connection.

#### Scenario: Unknown host
- **WHEN** a job sends CONNECT for a host that no proxy rule names
- **THEN** the proxy answers 403 before any upstream connection and writes the blocked line with host and path.

### Requirement: Request identity check

When the SNI or Host header differs from the CONNECT host, the job proxy SHALL refuse the connection.

#### Scenario: Mismatched identity
- **WHEN** the SNI or Host header of a tunnel request differs from the CONNECT host
- **THEN** the proxy refuses the connection without an upstream dial.

### Requirement: Non-canonical path

When a request carries a path that is not canonical, the job proxy SHALL refuse the request without normalization.

#### Scenario: Dot segments
- **WHEN** a request carries a path that is not canonical
- **THEN** the proxy refuses the request and logs the host with a fixed reason and no raw path.

### Requirement: Denied address refused

When a tunnel host resolves to an address that the outbound guard denies, the job proxy SHALL refuse the connection to that address.

#### Scenario: Resolved denied address
- **WHEN** a named tunnel host resolves to an address that the outbound guard denies
- **THEN** the proxy refuses the request and writes the blocked line with host and path.

### Requirement: Bound dial

When the job proxy dials a tunnel host, the job proxy SHALL connect only to a resolved address that it checked with the outbound guard.

#### Scenario: Checked address dialed
- **WHEN** a tunnel request passes the proxy rule and guard checks
- **THEN** the proxy connects only to an address that it checked.

#### Scenario: Mixed addresses
- **WHEN** a host resolves to one allowed and one denied address
- **THEN** the proxy uses only an allowed checked address and never dials a denied or unchecked address.

### Requirement: No rebind between check and connect

When the connect address differs from the guard-checked address, the job proxy SHALL refuse the dial.

#### Scenario: Rebound address
- **WHEN** the address at connect time differs from the checked address or maps to a denied range
- **THEN** the dial fails closed and the proxy writes the blocked line.

### Requirement: Upstream proxy carries connection

Where a pool names an upstream proxy, the job proxy SHALL still apply the outbound guard to the resolved address of each rule host.

#### Scenario: Guard still applies
- **WHEN** a pool names an upstream proxy for a proxied phase
- **THEN** the proxy checks the resolved rule host with the outbound guard and opens the upstream connection only on allow.

### Requirement: Fixed reasons

When the job proxy refuses or fails a request, the job proxy SHALL label it with one fixed reason from its reason map.

#### Scenario: Fixed reason used
- **WHEN** the job proxy refuses or fails a request
- **THEN** the blocked line carries one value from the fixed reason map and no library error text.

### Requirement: Blocked line in job log

When the job proxy refuses a request, the job proxy SHALL write blocked by the job proxy with the host and the path to the job log.

#### Scenario: Blocked request logged
- **WHEN** the job proxy refuses a request
- **THEN** the job log holds blocked by the job proxy with the host and the canonical path, a fixed reason, and no header, body, query value, or credential.

#### Scenario: Hostile log value
- **WHEN** a refused request carries line breaks or a non-canonical path
- **THEN** the log holds one line with stripped line breaks and no raw non-canonical path.
