## ADDED Requirements

### Requirement: Compare-and-swap append

When two instances call AppendChain on one audit scope, the server SHALL store an entry through AppendChain only when the expected head matches the stored head in the same transaction.

#### Scenario: Concurrent AppendChain appends
- **WHEN** two instances call AppendChain on one audit scope with the same expected head
- **THEN** one AppendChain call stores and moves the head by one, and the other AppendChain call gets a head mismatch with the new head and no partial write

### Requirement: Mismatch body and retry

When the expected head from AppendChain does not match, the server SHALL answer 409 with the new head in the error shape of the API.

#### Scenario: Client retry after AppendChain mismatch
- **WHEN** a caller gets a 409 with the new head from AppendChain and sends again with that head
- **THEN** the second AppendChain call stores with the new head and the chain check passes for both entries

### Requirement: Fenced append

When the server appends an audit entry in a namespace through AppendChain, the server SHALL check the namespace fence in the same transaction as the head check.

#### Scenario: AppendChain with stale fence
- **WHEN** a caller calls AppendChain with a stale namespace fence
- **THEN** the AppendChain transaction changes zero rows and answers a fence error

### Requirement: Authorized append

When a caller calls AppendChain on an audit scope, the server SHALL allow the write only with the grant on the namespace at the point of use.

#### Scenario: AppendChain without grant
- **WHEN** a caller without the grant on the namespace calls AppendChain
- **THEN** the server refuses the AppendChain call and moves no head

### Requirement: Chain check after race

While an AppendChain race resolves, the server SHALL keep the audit hash chain check passing for the stored entries.

#### Scenario: Verify after AppendChain race
- **WHEN** two concurrent AppendChain calls complete with a retry
- **THEN** the chain check passes and lists both entries in sequence order
