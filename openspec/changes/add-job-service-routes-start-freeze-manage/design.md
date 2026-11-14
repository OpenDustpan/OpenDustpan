## Context

Job start must stop while a namespace freeze is active. Freeze needs create, end, and list routes. Break-glass start during a freeze needs a separate namespace break-freeze permission.

## Decisions

1. **Freeze as namespace record keyed by stable ID.** Freeze is stored per namespace ID with creator, reason of at most 500 characters, start time, and end record. Job start reads the active freeze in the same transaction as dispatch, so a freeze made between check and dispatch still stops the start.
2. **Single active freeze per namespace.** Only one active freeze exists per namespace. A second create is refused, and an end with no active freeze is refused.

## Structure

Unexported freeze gate type with constructor NewFreezeGate that loads the active freeze for the namespace ID in the start transaction; only the constructor builds the gate, and job start calls Allow before dispatch.

## Risks

- A caller bypasses the route and calls job start directly during a freeze.
- Two ends of one freeze interleave and leave the freeze state unclear.

## Layers (defense in depth)

| Layer | Guard | Hostile test |
| --- | --- | --- |
| API authorization and input check | Route requires the namespace freeze manage action for create and end, and the namespace read action for list. Reason longer than 500 characters is refused. | Call create, end, and list with a principal that holds no grant and check that each route answers 403 and changes nothing. Send a reason of 501 characters and check that create answers 400. |
| Service guard | Job start service checks the active freeze in the same transaction as dispatch and refuses start while a freeze is active unless the caller holds namespace break-freeze with a reason of at most 500 characters. | Start a job during an active freeze with and without break-freeze, and create a freeze between check and dispatch, and check that only the break-freeze caller starts. |
