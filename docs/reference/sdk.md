# Embed the server

`pkg/dustpan` is the Go SDK of the server. A program can build a server with
`dustpan.NewServer` and serve it with `Run`. `Run` returns when the caller
cancels the context. `Close` then closes the built-in store that `WithSQLite`
or `WithPostgreSQL` opened.

```go
store, err := dustpan.OpenStore(ctx, "sqlite:/var/lib/opendustpan/dustpan.db")
if err != nil {
	return err
}

defer store.Close()

server, err := dustpan.NewServer(
	dustpan.WithBuiltinStore(store),
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

`OpenStore` opens the built-in SQLite or PostgreSQL store and answers a
`*dustpan.BuiltinStore`. The database takes the forms `sqlite:<path>`,
`postgres://...` and `postgresql://...`. `WithBuiltinStore` sets every store
option of the server from the handle. The caller closes a store that it
opened with `OpenStore`. The server does not close it. `WithSQLite(path)` and
`WithPostgreSQL(dsn)` open the store in one option and the server closes it.

A third-party store uses the `With...Store` options and the conformance
suites of the ports. The store options of a built-in store apply after all
other options, so they replace a `With...Store` option for the same port.
