## Context

The enforcement part owns expiry calculation and disable. This part computes a read-only warning state from expiry and present time and shows it in the API, the console data, and both CLIs.

## Decisions

1. **Warning state is read-only.** The token read path computes warning state from expiry time and the two warning thresholds. No write path changes. The API returns expiresAt, seconds left, and warning level in the same token response.
2. **One warning source for API, console, and CLIs.** The CLI warning comes from the tool-compatible API response headers or body that both the Pulumi CLI and OpenTofu already read. No new CLI binary ships. The console banner reads the same API field.
3. **Threshold boundary rule.** Threshold comparison is inclusive at each boundary. Level urgent wins when both windows match. A threshold of 0 disables that window. When the second threshold is larger than the first, the larger window decides early and the smaller decides urgent.

## Structure

unexported expiryWarning value made only by newWarningState with checked expiry time and threshold settings

## Risks

- A CLI that ignores the warning field shows no warning while the API reports one.
- Clock skew between server and client shifts the day count shown in the CLI.
