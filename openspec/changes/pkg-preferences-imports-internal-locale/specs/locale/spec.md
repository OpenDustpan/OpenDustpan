## ADDED Requirements

### Requirement: Language check

Where the NEW package pkg/locale is imported, the server SHALL accept the same language values as on main.

#### Scenario: Valid language passes
- **WHEN** the caller passes a language value accepted on main
- **THEN** NEW ValidateLanguage returns nil

#### Scenario: Invalid language fails
- **WHEN** the caller passes an invalid language value
- **THEN** NEW ValidateLanguage returns an error that carries preferences.field-invalid

### Requirement: Formatting locale check

Where the NEW package pkg/locale is imported, the server SHALL accept the same formattingLocale values as on main.

#### Scenario: Valid locale passes
- **WHEN** the caller passes a formattingLocale value accepted on main
- **THEN** NEW ValidateFormattingLocale returns nil

#### Scenario: Invalid locale fails
- **WHEN** the caller passes an invalid formattingLocale value
- **THEN** NEW ValidateFormattingLocale returns an error that carries preferences.field-invalid

### Requirement: Digits check

Where the NEW package pkg/locale is imported, the server SHALL accept the same digits values as on main.

#### Scenario: Valid digits pass
- **WHEN** the caller passes a digits value accepted on main
- **THEN** NEW ValidateDigits returns nil

#### Scenario: Invalid digits fail
- **WHEN** the caller passes an invalid digits value
- **THEN** NEW ValidateDigits returns an error that names the valid values

### Requirement: Time zone check

Where the NEW package pkg/locale is imported, the server SHALL check timeZone against the embedded database only.

#### Scenario: Valid time zone passes
- **WHEN** the caller passes a time zone from the embedded database
- **THEN** NEW ValidateTimeZone returns nil

#### Scenario: Unknown time zone fails
- **WHEN** the caller passes a time zone outside the embedded database
- **THEN** NEW ValidateTimeZone returns an error that carries preferences.field-invalid
