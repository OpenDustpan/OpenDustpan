# Embed the server

`pkg/dustpan` is the Go SDK of the server. A program can build a server with
`dustpan.NewServer`, start it with `Run`, and stop it with `Close`.

```go
server, err := dustpan.NewServer(
	dustpan.WithSQLite("/var/lib/opendustpan/dustpan.db"),
	dustpan.WithInstallation("eu-west"),
	dustpan.WithBlobStore(blobs),
	dustpan.WithAuthenticator(authn),
	dustpan.WithSecretWrapper(wrapper),
)
if err != nil {
	return err
}

defer server.Close()

return server.Run(ctx)
```

`WithSQLite` opens the built-in SQLite store and sets every store option of
the server from it. `WithPostgreSQL(dsn)` does the same with the PostgreSQL
store. A third-party store uses the `With...Store` options and the conformance
suites of the ports. The store options of a built-in store apply after all
other options, so they replace a `With...Store` option for the same port.
