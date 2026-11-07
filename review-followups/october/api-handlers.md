# Review follow-ups (October): API handlers

## 1. Expose a served boolean for each release in the management versions list,
PR #1306

- Files: internal/api/registry/handlers.go:69; internal/api/registry/handlers.go:360
- Change: Expose a served boolean for each release in the management versions list, derived from the protocol's current eligibility rules. Retain unserved releases in the management list.
- Acceptance: Add an API test showing a release as served before signing-key removal and unserved afterward. Verify the release remains listed and the indicator agrees with protocol availability.

## 2. Preserve JSON field presence for operator-owned fields and reject their
PR #1311

- Files: internal/api/server/githost_connections.go:51; internal/api/server/githost_connections.go:66; internal/api/server/githost_connections.go:296
- Change: Preserve JSON field presence for operator-owned fields and reject their presence regardless of whether their value is null.
- Acceptance: Request tests send apiAddress, appID, privateKey, eventSecret, credential and credentials individually with null and non-null values and expect 400. Otherwise-valid requests omitting those fields still succeed.

## 3. Handle forge.ErrInvalid without exposing internal wrapping prefixes
PR #1311

- Files: internal/api/server/githost.go:212
- Change: Handle forge.ErrInvalid without exposing internal wrapping prefixes. Preserve the public sentinel text and validation reason.
- Acceptance: A handler test supplies an ErrInvalid with a validation reason and multiple internal wrappers. The 400 response includes the public reason but excludes the wrapper prefixes.

## 4. Carry the target connection ID through connectionChange and include it in
PR #1311

- Files: internal/api/server/githost_connections.go:348; internal/api/server/githost_connections.go:376; internal/api/server/githost_connections.go:445; internal/api/server/githost_connections.go:453
- Change: Carry the target connection ID through connectionChange and include it in refused-operation audit entries when available. Preserve namespace scope and avoid inventing an ID for failed creates.
- Acceptance: Audit tests verify that refused delete and make-default operations identify the requested connection. Failed creates record no fabricated connection ID.

## 5. Include a usable sign-in start path in the reauthenticate field when fresh
PR #1322

- Files: internal/api/pulumi/freshness.go:49-51; internal/api/pulumi/freshness.go:99-103
- Change: Include a usable sign-in start path in the reauthenticate field when fresh authentication is refused with 403. Preserve the existing status, error code and no-store behavior.
- Acceptance: Add a test sending an access token to a fresh-authentication-protected Pulumi route and assert that its 403 response includes a usable sign-in start path. Verify that stale-session responses retain their existing 401 status and provider-specific fresh-sign-in path.

## 6. Extend the nullable rule to maps: a nil map without omitempty is written as
PR #1338

- Files: internal/api/apidesc/schema.go:517; internal/api/apidesc/schema_check.go:27
- Change: Extend the nullable rule to maps: a nil map without omitempty is written as null although its schema is a non-nullable object. Either declare maps nullable or omit them when nil.
- Acceptance: A test in internal/api/apidesc shows that a nil map field without omitempty follows the chosen rule in both the schema and the JSON output.

## 7. Reject dustpan:"required" on fields promoted through embedded pointers during
PR #1338

- Files: internal/api/apidesc/fields.go:53; internal/api/apidesc/fields.go:229
- Change: Reject dustpan:"required" on fields promoted through embedded pointers during field discovery. Return a descriptive error instead of silently making the field optional.
- Acceptance: Add a schema test whose embedded pointer promotes a required field and assert a descriptive error. Ordinary required fields and supported embedded fields must remain valid.

## 8. Preserve every checked action-kind pair in the authorization assertions instead
PR #1338

- Files: internal/api/jobs/routes_test.go:848; internal/api/objectcontext/routes_test.go:628
- Change: Preserve every checked action-kind pair in the authorization assertions instead of retaining only one kind per action. Sharing the helper is optional.
- Acceptance: Exercise one action checked against two different object kinds and verify that both are asserted regardless of order. An incorrect kind in either check must fail the test.

## 9. Record a diagnostic containing the sink ID when response conversion rejects a
PR #1350

- Files: internal/api/notifications/handlers.go:83-90
- Change: Record a diagnostic containing the sink ID when response conversion rejects a stored webhook target. Preserve the empty response target and exclude the stored target, URL and secrets from the diagnostic.
- Acceptance: Exercise response conversion with a malformed restored webhook target. Assert an empty response target and a diagnostic identifying the sink without exposing its target, URL or secrets.

## 10. Add a sink-ID diagnostic when the defensive host check rejects a legacy stored
PR #1350

- Files: internal/api/notifications/handlers.go:83-90
- Change: Add a sink-ID diagnostic when the defensive host check rejects a legacy stored webhook target. Keep response redaction and do not record URLs or secret material.
- Acceptance: Test a legacy webhook target that fails response-time host validation. Verify the response remains redacted and the diagnostic contains the sink ID but no URL or secrets.

## 11. Qualify the removal guarantee to require that the stored trigger still matches
PR #1392

- Files: internal/api/triggers/target.go:96; internal/api/triggers/target.go:128
- Change: Qualify the removal guarantee to require that the stored trigger still matches the namespace and unit ID resolved from the request. Do not promise deletion through this route for corrupt unit IDs that cannot match a path unit.
- Acceptance: The documentation explicitly distinguishes removable invalid fields from identity corruption that prevents the resolver from reaching deletion. Preserve the same-unit restriction.

## 12. Use one shared reserved-prefix rule for jobs API keys and trigger fire keys,
PR #1392

- Files: internal/api/jobs/start.go:245; internal/api/triggers/fire_key_test.go:173
- Change: Use one shared reserved-prefix rule for jobs API keys and trigger fire keys, including consistent case handling. Reuse the shared prefix definition rather than a local duplicate.
- Acceptance: Add matching lowercase, mixed-case, and uppercase prefix cases for both entry points and verify identical reservation decisions. Legitimate server-generated drift keys must remain usable.

## 13. Exclude foreign-namespace references before constructing the unit-name
PR #1489

- Files: internal/api/inventory/filters.go:89; internal/api/inventory/filters.go:103; internal/api/inventory/views.go:448
- Change: Exclude foreign-namespace references before constructing the unit-name constraint. Preserve RestrictUnits when supplied references select no local units.
- Acceptance: Test foreign-only and mixed-namespace saved-view references, including local namesakes. Foreign references must not select local namesakes, and foreign-only references must produce an empty constrained query.

---
_Generated by [Claude Code](https://claude.ai/code)_
