## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in the pull request body before the first test. Check: the pull request body lists the differences, or says that there are none.

## 1. Failing tests first

- [ ] 1.1 Add failing publish test in NEW file internal/registrysvc/plugin_publish_test.go for plugin archive set, registry-safe name, and upload checks. Check: TestPluginPublish_ChecksSignatureAndName fails on main, passes after fix.
- [ ] 1.2 Add failing download test in NEW file internal/registrysvc/plugin_download_test.go for valid name, version, OS, and arch on the new plugin route. Check: TestPluginDownload_StreamsStoredArchive fails on main, passes after fix.
- [ ] 1.3 Add failing test in NEW file internal/registrysvc/plugin_verdict_test.go that a bad signature or bad checksum version returns 403 with no bytes. Check: TestPluginDownload_BadVerdictRefused fails on main, passes after fix.
- [ ] 1.4 Add failing tests in NEW file internal/registrysvc/plugin_auth_test.go for 401 on missing token and 404 on no read access or cross-namespace URL. Check: TestPluginDownload_AuthAndIsolation fails on main, passes after fix.
- [ ] 1.5 Add failing tests in NEW file internal/registrysvc/plugin_stream_test.go for stream SHA-256 mismatch, mid-stream blob failure, and audit entries on success and refusal. Check: TestPluginDownload_StreamIntegrityAndAudit fails on main, passes after fix.

## 2. Build

- [ ] 2.1 Wire publish record to the new plugin download route with registry read actions and audit entries. Declare the route in internal/api/apidesc/routes_plugins.go (NEW) and write its adapter in internal/registrysvc/plugin_http.go (NEW). Check: GET on the new plugin download route under /api/dustpan/v1/namespaces/{}/registry with a personal access token streams bytes; bad version gives 403 that names the artifact.
- [ ] 2.2 Add e2e step in NEW file e2e/pulumi_plugin_test.go against the real Pulumi CLI on SQLite and PostgreSQL. It publishes through PUT /api/dustpan/v1/namespaces/{}/registry/providers/{}/versions/{} (existing route; declared in internal/api/apidesc/routes_plugins.go (NEW) with its adapter in internal/registrysvc/plugin_http.go (NEW)) and installs through the new plugin download route under /api/dustpan/v1/namespaces/{}/registry (declared in internal/api/apidesc/routes_plugins.go (NEW); adapter in internal/registrysvc/plugin_http.go (NEW)), both with a personal access token. Check: E2E `pulumi plugin install` over TLS against the new plugin download route with a personal access token passes on both dialects. Confirmed by: CI (the agent writes the e2e test).
- [ ] 2.3 Write docs page in NEW file docs/registry/pulumi-plugins.md for publish and install of a private Pulumi plugin. Check: Docs page command sequence runs as written in a test. Confirmed by: CI (an e2e step in the NEW file e2e/artifact_registry_private_pulumi_plugins_docs_test.go runs each command of the docs page against the e2e test server).

## 3. Side effects (computed from main)

- [ ] 3.1 Regenerate `internal/api/apidesc/openapi.json` (`go test ./pkg/dustpan -run TestAPIDescriptionGolden -update-api-description`) and then the clients (`make clients-generate`). Never edit a generated file by hand. Check: the named files change in the same pull request. Confirmed by: the driver's gates.

## 4. Definition of done
