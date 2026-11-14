## Context

The runner starts one launcher for one claimed phase or sweep. The launcher stays small, static, and free of store and forge code, and runs steps with no shell and with masked logs.

## Decisions

1. **Thin binary over pkg/launcher.** Keep all run logic in pkg/launcher and keep cmd/dustpan-launcher as a thin main that parses flags, selects the mode, and returns the exit code.
2. **Static release build with a dependency check.** Build with CGO_ENABLED=0 for linux amd64 and linux arm64, check the result is static with file output, and check go list -deps holds no SQL driver and no forge client, including transitive imports.

## Structure

Unexported launcherSession built only by Parse plus Open, so no caller makes a run without parsed flags, a checked memory volume, and a masker; cmd holds no logic that a test can bypass.

## Risks

- A static check passes while a new import pulls a SQL driver through a helper package.
- A helper mode writes credentials to a path outside the memory-backed directory.

## Layers (defense in depth)

| Layer | Guard | Hostile test |
| --- | --- | --- |
| Binary boundary | Strict flag parser accepts only the flags of ADR 134 decision 3 and ends with code 64 on an unknown, repeated, or valueless flag. | Run with an unknown flag, a repeated flag, and a flag without a value and check each ends with code 64. |
| Credential containment | Memory-backed secrets directory check plus 0700 owned directory, 0600 files, per-step directory removed at step end, and dumpable set to 0. | Point secrets at a non-tmpfs path and at a pre-made directory with wrong mode and check the run refuses before any step starts. |
| Log masking | Masker replaces each session secret of 4 or more bytes with [masked] across chunk ends before the log leaves the launcher. | Emit a secret split across two log chunks and check the stored log holds [masked] and no plain secret. |
