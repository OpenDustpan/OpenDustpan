//go:build e2e

// Package e2e drives real Pulumi CLI and OpenTofu binaries against an
// OpenDustpan server and records every HTTP request they make.
//
// Neither the Pulumi CLI API nor the TFE-compatible API that OpenTofu's cloud
// block speaks is a published specification, so this suite is the only
// authority on which endpoints and response shapes the clients expect. It is
// excluded from `make test` by the e2e build tag; run it with `make e2e`.
//
// Each CLI version in the matrix gets its own server, database and home
// directory for every store under test, so versions and stores never share
// state. SQLite runs use a temporary file; PostgreSQL runs use an isolated
// schema that is dropped when the run ends. OpenTofu gets an HTTPS server,
// because its service discovery refuses plain HTTP; that part runs on Linux
// only. CLI archives are downloaded once into a cache directory and verified
// against sha256 checksums before use.
//
// With E2E_SERVER=binary the server is the real cmd/opendustpan binary,
// built once and run with --tls-cert, --tls-key, a database and a blob
// directory, fronted by a recording proxy. With E2E_BLOBS=s3 the binary gets
// --blob-store with --s3-endpoint instead of the blob directory, and the S3
// test credentials as AWS_ACCESS_KEY_ID and AWS_SECRET_ACCESS_KEY. The CLI
// trusts the proxy through SSL_CERT_FILE with a generated certificate. The
// binary variant is the proof for the milestone 2 exit test. Without the
// variable the suite runs the same scenarios against an in-process TLS server
// for fast runs, with the blob/s3 store in process for E2E_BLOBS=s3.
//
// In the binary mode the environment keeps the binary path, the backend
// settings (database URL, blob directory, certificate and key files, tokens)
// and the backend port. Env.DatabaseURL and Env.BlobDir offer the flag
// values, so a test does not rebuild them. Env.RunBinary runs any
// `opendustpan` subcommand as a child process against that database and blob
// directory and returns its stdout, stderr and exit code; a non-zero exit is
// a result, not a test failure. Env.StopServer ends the child with SIGTERM
// and checks exit status 0. Env.StartServer starts the same binary again on
// the same port, database, blob directory and certificate, so the recording
// proxy target and the saved CLI logins stay valid. RunBinary, StopServer
// and StartServer need E2E_SERVER=binary and fail the test otherwise.
//
// With E2E_INSTANCES=N (N above 1) every binary environment starts N server
// binaries of one installation on one PostgreSQL database and one blob store
// (the blob directory, or the S3-compatible bucket of E2E_BLOBS=s3), behind a
// round-robin TLS proxy of the suite. The proxy checks /health/ready of each
// instance every 250 ms, sends each request to the next healthy instance,
// sends a request again to another instance only when that is safe, and
// records the instance of each request (Entry.Backend). Env.HA gives a test
// the cluster, with Kill, Stop, Start and the logs of each instance.
// TestHighAvailability uses it (make e2e-ha). A test that stops the server to
// write to the database stops and starts every instance.
//
// Environment variables:
//
//	E2E_PULUMI_VERSIONS  comma-separated versions, "latest", or "pinned" for the
//	                     pinned versions of the default (default: see Pulumi)
//	E2E_TOFU_VERSIONS    comma-separated versions, "latest", or "pinned" for the
//	                     pinned versions of the default (default: see OpenTofu)
//	E2E_DATABASES        comma-separated stores: sqlite, postgres (default: sqlite,
//	                     plus postgres when DUSTPAN_TEST_POSTGRES_URL is set)
//	DUSTPAN_TEST_POSTGRES_URL  PostgreSQL URL for the postgres store; each run gets its own
//	                           schema (default: postgres runs are skipped)
//	E2E_BLOBS            live blob store of every run: fs (default, a directory) or
//	                     s3 (an S3-compatible bucket with a unique prefix per run)
//	DUSTPAN_TEST_S3_ENDPOINT, DUSTPAN_TEST_S3_BUCKET, DUSTPAN_TEST_S3_ACCESS_KEY,
//	DUSTPAN_TEST_S3_SECRET_KEY  S3-compatible server for E2E_BLOBS=s3; each run
//	                           gets its own key prefix that is removed at the end
//	                           (default: S3 runs are skipped)
//	E2E_INSTANCES        number of server binaries of one installation, 1 to 9 (default 1);
//	                     more than 1 needs PostgreSQL and runs the server binary
//	E2E_HA_BACKUP_MB     megabytes of checkpoint data that the backup step of
//	                     TestHighAvailability writes before the kill (default 500)
//	E2E_CACHE_DIR        where CLI archives are cached (default: user cache dir)
//	E2E_REPORT_DIR       where the request log and summary are written (default: ./report)
//	E2E_SERVER           "binary" runs the real server binary with TLS flags (default:
//	                     in-process server; the binary variant is the exit-test proof)
package e2e
