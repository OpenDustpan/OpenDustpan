//go:build e2e

package e2e

import (
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"opendustpan.org/opendustpan/pkg/dustpan"
	"opendustpan.org/opendustpan/pkg/store/postgres"
	"opendustpan.org/opendustpan/pkg/store/sqlite"
)

// registryStoreOptions returns the option that gives the in-process server the
// registry stores of st.
func registryStoreOptions(st serverStore) []dustpan.Option {
	switch s := st.(type) {
	case *sqlite.Store:
		return []dustpan.Option{dustpan.WithRegistry(s.Registry(), s.RegistryKeys())}
	case *postgres.Store:
		return []dustpan.Option{dustpan.WithRegistry(s.Registry(), s.RegistryKeys())}
	default:
		return nil
	}
}

// publicURLOptions returns the option that sets the public URL, or none for an
// empty URL.
func publicURLOptions(raw string) []dustpan.Option {
	if raw == "" {
		return nil
	}

	return []dustpan.Option{dustpan.WithPublicURL(raw)}
}

// inprocessPublicURL returns the public URL of an in-process server that
// OpenTofu talks to. The provider registry protocol compares each request with
// the host of this URL. Other servers keep no public URL, as before.
func inprocessPublicURL(in envInput, ts *httptest.Server) string {
	if in.cli.Tool != ToolTofu || in.signIn {
		return ""
	}

	return "https://" + ts.Listener.Addr().String()
}

// WriteRegistryProject writes main.tf in a directory of its own, apart from the
// cloud-block part, and returns the directory. The part holds the providers
// that the test installs from the registry of the server.
func (e *Env) WriteRegistryProject(tb testing.TB, name, mainTF string) string {
	tb.Helper()

	dir := filepath.Join(filepath.Dir(e.project), name)
	if err := os.MkdirAll(dir, dirMode); err != nil {
		tb.Fatalf("create %s: %v", dir, err)
	}

	e.write(tb, filepath.Join(dir, "main.tf"), mainTF)

	return dir
}

// RegistryAddress returns the address of a provider of the registry of the
// default namespace, as OpenTofu writes it in a configuration: the host of the
// server, the namespace and the provider name.
func (e *Env) RegistryAddress(name string) string {
	return e.Host() + "/" + Namespace + "/" + name
}
