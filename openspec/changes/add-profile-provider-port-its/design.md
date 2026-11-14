## Context

Profile open calls provider blocks for login, secrets and outputs. Each adapter talks to an outside remote. One shared port, one suite and one guarded outbound client keep each adapter inside the same safety rules.

## Decisions

1. **Ports and suites in the profile domain.** Add pkg/profile/provider with Provider, Rotator, TokenSource, Spec, sentinels ErrRemoteDenied, ErrRemoteNotFound, ErrUnavailable and ErrTooLarge, and pkg/profile/provider/providertest with RunProvider and RunRotator against a fake remote.
2. **Adapter registry keyed by kind and name.** Add a registry in the wiring that maps kind plus name to one adapter and refuses a duplicate pair at server start with a plain error that names the pair. A block with an unknown pair fails at save and at open with ErrInvalid.
3. **One guarded outbound client.** Route each adapter call through internal/egress/egresshttp with NoRedirect, HTTPSOnly, pinned dial guard, 1 MiB read limit, 256 KiB request body limit, 10 second default timeout with 30 second maximum, and at most 16 parallel calls per host per server process with wait within the timeout.

## Structure

Unexported type providerRegistry with constructor newProviderRegistry that checks duplicate kind plus name pairs and checks that each adapter exposes Spec, Check, Open and Close; the constructor makes the map, so a duplicate cannot start.

## Risks

- A new adapter bypasses the shared client and uses http.DefaultClient.
- A duplicate kind plus name pair starts silently and shadows the first adapter.
- A secret value reaches the job log through an unmasked provider message.

## Layers (defense in depth)

| Layer | Guard | Hostile test |
| --- | --- | --- |
| port adapter | Port validates Spec at save and Check at open; secret inputs as plain literals refused; timeout 10 seconds by default and at most 30 seconds. | Send a block with a plain literal for a secret input and a 256 KiB over-limit request; the open must refuse with ErrInvalid without network use. |
| service | Service maps remote errors to sentinels first with ErrProvider mapping, enforces 16 parallel calls per host per server process with wait inside the timeout, caps messages at 200 characters and masks secret inputs in stored messages and in the job log. | Drive the fake to return denied, missing, 5xx, timeout and oversize answers plus a message that holds a secret input; each maps to the correct sentinel and stores no secret value. |
| HTTP handler | Outbound client refuses link-local, loopback and unlisted private addresses at dial, allows a listed host:port for its namespaces, pins the resolved IP for the whole fetch, follows no cross-host redirect and caps the read at 1 MiB. | Request 169.254.169.254, 127.0.0.1, 10.x and an unlisted private host plus a cross-host redirect and a rebinding DNS answer; each dial must fail with ErrUnavailable. |
