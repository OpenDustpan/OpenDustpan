## ADDED Requirements

### Requirement: Refuse variation selectors

When a new name holds a variation selector in U+FE00 to U+FE0F or U+E0100 to U+E01EF, the server SHALL refuse the name.

#### Scenario: Selector refused
- **WHEN** a new name holds U+FE00 or U+E0100
- **THEN** the server refuses the name

#### Scenario: Boundary passes
- **WHEN** a name holds U+FDFF, U+FE10 or U+E01F0
- **THEN** IsHiddenRune returns false

### Requirement: Refuse Hangul fillers

When a new name holds U+115F, U+1160, U+3164 or U+FFA0, the server SHALL refuse the name.

#### Scenario: Filler refused
- **WHEN** a new name holds only U+3164
- **THEN** the server refuses the name

#### Scenario: Syllable passes
- **WHEN** a name holds a complete Hangul syllable
- **THEN** the server accepts the name

### Requirement: Refuse combining grapheme joiner

When a new name holds U+034F, the server SHALL refuse the name.

#### Scenario: Joiner refused
- **WHEN** a new name holds U+034F
- **THEN** the server refuses the name

### Requirement: Keep stored names usable

While a name was stored before the wider refusal, the server SHALL keep it readable and usable for sign-in.

#### Scenario: Stored name signs in
- **WHEN** a stored name holds a now-refused rune
- **THEN** sign-in and read still work

#### Scenario: Rename validates
- **WHEN** a rename sets a value with a refused rune
- **THEN** the server refuses the rename
