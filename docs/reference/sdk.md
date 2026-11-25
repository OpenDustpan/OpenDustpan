# Embed the server

`pkg/dustpan` is the Go SDK of the server. A program can build a server with
`dustpan.NewServer` and serve it with `Run`. `Run` returns when the caller
cancels the context. `Close` then closes the built-in store that `WithSQLite`
or `WithPostgreSQL` opened.

This program starts a server on `127.0.0.1:18080` with one store and its settings. Make a
token with `openssl rand -hex 32` and set it in `EXAMPLE_TOKEN`. Run the
program with one argument, a directory that exists:
`EXAMPLE_TOKEN=<token> go run . /var/lib/example`. Stop it with Ctrl-C. A
request to `/api/user` with the header `Authorization: token <token>` shows the
administrator. A token shorter than 32 bytes fails at start with the command
that makes one.

```go
package main

import (
	"context"
	"crypto/rand"
	"fmt"
	"os"
	"os/signal"

	"opendustpan.org/opendustpan/pkg/blob/fs"
	"opendustpan.org/opendustpan/pkg/dustpan"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/secret"
)

func run(ctx context.Context, dir string) error {
	store, err := dustpan.OpenStore(ctx, "sqlite:"+dir+"/dustpan.db")
	if err != nil {
		return err
	}

	defer store.Close()

	blobs, err := fs.Open(dir + "/blobs")
	if err != nil {
		return err
	}

	authn, err := identity.NewStaticToken("admin", os.Getenv("EXAMPLE_TOKEN"))
	if err != nil {
		return err
	}

	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		return err
	}

	wrapper, err := secret.NewBuiltinWrapper(key)
	if err != nil {
		return err
	}

	server, err := dustpan.NewServer(
		dustpan.WithBuiltinStore(store),
		dustpan.WithSettings(store.SettingStore(), store.SettingDeclarations(), nil),
		dustpan.WithInstallation("eu-west"),
		dustpan.WithBlobStore(blobs),
		dustpan.WithAuthenticator(authn),
		dustpan.WithSecretWrapper(wrapper),
		dustpan.WithListenAddr("127.0.0.1:18080"),
	)
	if err != nil {
		return err
	}

	defer server.Close()

	return server.Run(ctx)
}

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	if err := run(ctx, os.Args[1]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
```

`OpenStore` opens the built-in SQLite or PostgreSQL store and answers a
`*dustpan.BuiltinStore`. The database takes the forms `sqlite:<path>`,
`postgres://...` and `postgresql://...`. `WithBuiltinStore` sets every store
option of the server from the handle. The caller closes a store that it
opened with `OpenStore`. The server does not close it. `WithSQLite(path)` and
`WithPostgreSQL(dsn)` open the store in one option and the server closes it.

A write through the ports of the handle (`secret.KeyStore`,
`localuser.PasswordStore`, `access.GrantStore`) skips the checks and the
audit entries of the services. Use the API for writes. Do not use these ports
in place of the API.

A third-party store uses the `With...Store` options and the conformance
suites of the ports. The store options of a built-in store apply after all
other options, so they replace a `With...Store` option for the same port.
