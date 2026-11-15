## ADDED Requirements

### Requirement: Alternating serve

While three instances serve one installation behind a round-robin proxy, the server SHALL serve each Pulumi request and each OpenTofu request on a healthy instance with no sticky session.

#### Scenario: Each instance serves each tool
- **WHEN** the suite runs the full Pulumi scenario and the full OpenTofu scenario through the round-robin TLS proxy across three instances
- **THEN** the proxy record shows each of three healthy instances served Pulumi requests and OpenTofu requests, and both full scenarios end with exit 0

#### Scenario: Requests alternate without sticky session
- **WHEN** one client sends a sequence of requests through the proxy
- **THEN** successive requests from one client land on different healthy instances

#### Scenario: Instance drop voids the run
- **WHEN** a health check removes one instance before the proxy record covers each instance for each tool
- **THEN** the suite voids the run and runs both scenarios again, and fails naming the instance after 3 voids

### Requirement: Lock handover across instances

While a workspace lock is held, the server SHALL accept its release on a different healthy instance.

#### Scenario: Lock releases through another instance
- **WHEN** one instance takes an OpenTofu workspace lock through the proxy and another instance releases it through the proxy
- **THEN** the release succeeds and the next plan shows the workspace unlocked
