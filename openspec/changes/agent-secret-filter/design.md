## Context

The model sees prompts and tool results. A model may put a secret into an answer or a tool call argument. The run record, the transcript and the logs would then hold it. The filter is the last layer between the model and the record. It must stop the attack with no help from the other layers.

## Decisions

1. `agent.SecretFilter` is immutable. `NewSecretFilter(values [][]byte)` copies the values. Values shorter than 8 bytes are not added to the exact list, because they match too much text. The pattern list still acts.
2. `Filter(text) (string, int)` replaces each exact value and each pattern match with `[removed: secret-like text]` and answers the count. It never answers an error: a failure to scan fails closed (decision 8).
3. The pattern list is in code and in a table in the docs. It has: known token prefixes, PEM blocks, JWT, `Authorization` and `Bearer` values, and `password=`, `secret=`, `token=` pairs. It has no entropy rule (decision 24).
4. The filter scans text, tool call arguments (the JSON strings), and readable reasoning. It does not read an opaque part and never changes it.
5. The wiring is one function in the loop, `agent.FilterModel(m Model, f SecretFilter) Model`. It wraps the model, so each model answer passes the filter before the loop sees it. The same function filters the prompt and each tool result before they reach the model. The probe calls `Filter` on a failure text that it builds.
6. The count of removals goes in the run record as a number. The record holds no part of a removed value.
7. The value check (decision 11): `ParseConnectionSpec` takes no secret values, so the check is a function `agent.CheckNoSecretValue(spec ConnectionSpec, values [][]byte) error`. The connect route calls it. It refuses a field that holds a secret item value, and names the field.
8. Fail closed: if the filter cannot scan a text (the text is above 4 MiB), it answers the marker for the whole text and counts 1.
9. A scan is linear in the text length. The patterns use the RE2 engine of the standard library, so a hostile text cannot make a scan slow.

## Structure

```go
// internal/agent/filter.go
type SecretFilter struct { /* unexported */ }
func NewSecretFilter(values [][]byte) SecretFilter
func (f SecretFilter) Filter(text string) (string, int)
func FilterModel(m Model, f SecretFilter) Model
func CheckNoSecretValue(spec ConnectionSpec, values [][]byte) error
const RemovedMarker = "[removed: secret-like text]"
```

## Risks

- The filter may remove a resource ID that looks like a secret. The pattern list has no entropy rule, and a corpus of IDs and hashes from state checks keeps them.
- A secret that a model writes in pieces (one character for each line) is not matched. The record still holds only text that the model wrote; the other layers (no value in a log) stay.

## Layers (defense in depth)

| Input | Layer | Guard | Hostile test (fails when the guard is removed) |
|---|---|---|---|
| model answer text | Output | `Filter` on text, tool call arguments and readable reasoning | `TestFilter_CanaryInAnswer`, `TestFilter_CanaryInToolArgs`, `TestFilter_CanaryInReasoning` |
| prompt and tool result | Output | `FilterModel` filters before the model | `TestFilterModel_PromptAndToolResult` |
| exact secret value | Output | Exact match of each secret item value | `TestFilter_ExactValue_Table` (each position, each encoding form that the list names) |
| secret-like text | Output | Pattern list | `TestFilter_Patterns_Corpus` |
| state IDs and hashes | Output | No entropy rule | `TestFilter_KeepsIDsCorpus` |
| opaque part | Store | Never read, never changed | `TestFilter_OpaqueUnchanged` |
| large text | Limit | Above 4 MiB the whole text is removed | `TestFilter_OversizeFailsClosed` |
| hostile text | Limit | RE2, linear time | `TestFilter_NoSlowPath` |
| document field | Boundary | `CheckNoSecretValue` | `TestCheckNoSecretValue_Table` |
| no off switch | Structure | No option, flag or field | `TestFilter_NoOffSwitch` (reflect over exported fields and options) |

Each check that looks redundant gets the comment: "Defense in depth: the record writer and the log layer hold no value by rule; this filter stays so that a new caller cannot skip it."
