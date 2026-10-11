// Command opendustpan runs the OpenDustpan server with the built-in adapters.
//
//	opendustpan serve [flags]
//	opendustpan namespace create [flags] <name>
//
// Every flag can also be set through the environment variable shown in its
// help text. The bootstrap admin token is read from DUSTPAN_ADMIN_TOKEN only,
// never from a flag, so it does not appear in process listings.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"strings"
	"syscall"

	dustpan "opendustpan.org/opendustpan"
	"opendustpan.org/opendustpan/identity"
	"opendustpan.org/opendustpan/namespace"
	"opendustpan.org/opendustpan/stack"
	"opendustpan.org/opendustpan/store/postgres"
	"opendustpan.org/opendustpan/store/sqlite"
)

var (
	errUsage          = errors.New("usage: opendustpan serve | opendustpan namespace create <name>")
	errUnknownBackend = errors.New("database must start with sqlite: or postgres://")
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)

	err := run(ctx, os.Args[1:], os.Stdout, os.Stderr)

	stop()

	if err != nil {
		fmt.Fprintln(os.Stderr, "opendustpan:", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	if len(args) == 0 {
		return errUsage
	}

	switch {
	case args[0] == "serve":
		return serve(ctx, args[1:], stderr)
	case len(args) >= 2 && args[0] == "namespace" && args[1] == "create":
		return createNamespace(ctx, args[2:], stdout, stderr)
	default:
		return errUsage
	}
}

// common holds the flags shared by every subcommand.
type common struct {
	database string
	region   string
}

func (c *common) register(fs *flag.FlagSet) {
	fs.StringVar(&c.database, "database", env("DUSTPAN_DATABASE", "sqlite:dustpan.db"),
		"sqlite:<path> or postgres://... (DUSTPAN_DATABASE)")
	fs.StringVar(&c.region, "region", env("DUSTPAN_REGION", "local"),
		"region this server is authoritative for (DUSTPAN_REGION)")
}

func serve(ctx context.Context, args []string, stderr io.Writer) error {
	var (
		c          common
		listen     string
		consoleDir string
		origins    string
	)

	fs := flag.NewFlagSet("serve", flag.ContinueOnError)
	fs.SetOutput(stderr)
	c.register(fs)
	fs.StringVar(&listen, "listen", env("DUSTPAN_LISTEN", ":8080"), "listen address (DUSTPAN_LISTEN)")
	fs.StringVar(&consoleDir, "console-dir", env("DUSTPAN_CONSOLE_DIR", ""),
		"serve a built web console from this directory (DUSTPAN_CONSOLE_DIR)")
	fs.StringVar(&origins, "allowed-origins", env("DUSTPAN_ALLOWED_ORIGINS", ""),
		"comma-separated console origins allowed to call the API (DUSTPAN_ALLOWED_ORIGINS)")

	if err := fs.Parse(args); err != nil {
		return fmt.Errorf("parse flags: %w", err)
	}

	logger := slog.New(slog.NewJSONHandler(stderr, nil))

	authn, err := identity.NewStaticToken("admin", os.Getenv("DUSTPAN_ADMIN_TOKEN"))
	if err != nil {
		return fmt.Errorf("DUSTPAN_ADMIN_TOKEN: %w", err)
	}

	st, err := openStore(ctx, c.database)
	if err != nil {
		return err
	}
	defer st.close()

	srv, err := dustpan.NewServer(
		dustpan.WithRegion(namespace.Region(c.region)),
		dustpan.WithNamespaceStore(st),
		dustpan.WithStackStore(st),
		dustpan.WithUpdateStore(st),
		dustpan.WithCheckpointStore(st),
		dustpan.WithEventStore(st),
		dustpan.WithAuthenticator(authn),
		dustpan.WithLogger(logger),
		dustpan.WithListenAddr(listen),
		dustpan.WithConsoleDir(consoleDir),
		dustpan.WithAllowedOrigins(splitList(origins)...),
	)
	if err != nil {
		return fmt.Errorf("configure server: %w", err)
	}

	if err := srv.Run(ctx); err != nil {
		return fmt.Errorf("run server: %w", err)
	}

	return nil
}

func createNamespace(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	var c common

	fs := flag.NewFlagSet("namespace create", flag.ContinueOnError)
	fs.SetOutput(stderr)
	c.register(fs)

	if err := fs.Parse(args); err != nil {
		return fmt.Errorf("parse flags: %w", err)
	}

	if fs.NArg() != 1 {
		return errUsage
	}

	name, err := namespace.ParseName(fs.Arg(0))
	if err != nil {
		return fmt.Errorf("namespace name: %w", err)
	}

	st, err := openStore(ctx, c.database)
	if err != nil {
		return err
	}
	defer st.close()

	svc, err := namespace.NewService(namespace.Config{Store: st, Now: nil, Region: namespace.Region(c.region)})
	if err != nil {
		return fmt.Errorf("namespace service: %w", err)
	}

	ns, err := svc.Create(ctx, name)
	if err != nil {
		return fmt.Errorf("create namespace: %w", err)
	}

	if _, err := fmt.Fprintf(stdout, "created namespace %s in region %s\n", ns.Name, ns.HomeRegion); err != nil {
		return fmt.Errorf("write output: %w", err)
	}

	return nil
}

// store is the persistence adapter chosen by --database.
type store interface {
	namespace.Store
	stack.Store
	stack.UpdateStore
	stack.CheckpointStore
	stack.EventStore
	close()
}

type sqliteStore struct{ *sqlite.Store }

func (s sqliteStore) close() {
	if err := s.Close(); err != nil {
		slog.New(slog.NewTextHandler(os.Stderr, nil)).Warn("close database", slog.String("error", err.Error()))
	}
}

type postgresStore struct{ *postgres.Store }

func (s postgresStore) close() { s.Close() }

//nolint:ireturn // The adapter is chosen at runtime, so the result must be an interface.
func openStore(ctx context.Context, database string) (store, error) {
	switch {
	case strings.HasPrefix(database, "sqlite:"):
		s, err := sqlite.Open(ctx, strings.TrimPrefix(database, "sqlite:"))
		if err != nil {
			return nil, fmt.Errorf("open sqlite store: %w", err)
		}

		return sqliteStore{s}, nil
	case strings.HasPrefix(database, "postgres://"), strings.HasPrefix(database, "postgresql://"):
		s, err := postgres.Open(ctx, database)
		if err != nil {
			return nil, fmt.Errorf("open postgres store: %w", err)
		}

		return postgresStore{s}, nil
	default:
		return nil, errUnknownBackend
	}
}

func env(key, fallback string) string {
	if v, ok := os.LookupEnv(key); ok {
		return v
	}

	return fallback
}

func splitList(s string) []string {
	var out []string

	for item := range strings.SplitSeq(s, ",") {
		if item = strings.TrimSpace(item); item != "" {
			out = append(out, item)
		}
	}

	return out
}
