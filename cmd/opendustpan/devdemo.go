package main

import (
	"context"
	"errors"
	"fmt"
	"io"

	"opendustpan.org/opendustpan/internal/localusersvc"
	"opendustpan.org/opendustpan/pkg/installation"
	"opendustpan.org/opendustpan/pkg/localuser"
	"opendustpan.org/opendustpan/pkg/localuser/argon2id"
	"opendustpan.org/opendustpan/pkg/localuser/blocklist"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/principal"
)

// demoNamespaces are the namespaces that the demo person administers: the
// namespace of dev and the two namespaces that the demo data uses.
//
//nolint:gochecknoglobals // A frozen list, never mutated.
var demoNamespaces = []namespace.Name{devNamespace, "demo-platform", "demo-data"}

// demoNamespaceNames answers the names of demoNamespaces as strings.
func demoNamespaceNames() []string {
	names := make([]string, 0, len(demoNamespaces))
	for _, name := range demoNamespaces {
		names = append(names, name.String())
	}

	return names
}

// errDemoStore reports a store that cannot keep the demo person.
var errDemoStore = errors.New("this store cannot keep the demo person")

// seedDemo sets up the demo person when --demo is set, and prints its sign-in
// once to stderr. Only dev passes the demo seed: serve has no flag, variable
// or key for it. Each start sets a new password.
func (r *devRun) seedDemo(ctx context.Context, in runInputs) error {
	st := in.st

	if !r.flags.demo {
		return nil
	}

	passwords, ok := st.(localuser.PasswordStore)
	grants, grantsOK := st.(localusersvc.DemoGrants)

	if !ok || !grantsOK {
		return errDemoStore
	}

	principals, err := principal.NewService(principal.Config{
		TempAdmins: nil,
		Store:      st,
		Guard:      principal.AdminOnlyGuard{},
		GroupReach: nil,
		Namespaces: nil,
		Tokens:     nil,
		Now:        nil,
		NewID:      nil,
	})
	if err != nil {
		return fmt.Errorf("demo principals: %w", err)
	}

	budget, err := argon2id.NewBudget(localuser.DefaultHashMemoryBudgetKiB)
	if err != nil {
		return fmt.Errorf("demo password hash budget: %w", err)
	}

	hasher, err := argon2id.New(budget)
	if err != nil {
		return fmt.Errorf("demo password hasher: %w", err)
	}

	seed, err := localusersvc.NewDemoSeed(localusersvc.DemoConfig{
		Passwords:  passwords,
		Principals: principals,
		Grants:     grants,
		Namespaces: st,
		Hasher:     hasher,
		Blocklist:  blocklist.Embedded(),
		Kind:       installation.KindDev,
		Now:        r.proc.now,
		Random:     nil,
	})
	if err != nil {
		return fmt.Errorf("demo seed: %w", err)
	}

	if namesErr := r.ensureDemoNamespaces(ctx, in); namesErr != nil {
		return namesErr
	}

	password, err := seed.Run(ctx, demoNamespaces)
	if err != nil {
		return fmt.Errorf("demo seed: %w", err)
	}

	return writeDemoSignIn(r.stderr, password)
}

// ensureDemoNamespaces makes the two demo namespaces that do not exist, and
// the placement rule in each one that it makes.
func (r *devRun) ensureDemoNamespaces(ctx context.Context, in runInputs) error {
	for _, name := range demoNamespaces[1:] {
		created, err := r.ensureNamed(ctx, in.st, name)
		if err != nil {
			return err
		}

		// As for the namespace dev, the rule comes with the namespace only.
		if created {
			if seedErr := r.seedPlacement(ctx, in, name); seedErr != nil {
				return seedErr
			}
		}
	}

	return nil
}

// writeDemoSignIn prints the sign-in of the demo person. The password is
// not a secret of an installation: dev sets a new one at each start.
func writeDemoSignIn(w io.Writer, password string) error {
	_, err := fmt.Fprintf(w, "Demo user: sign in with the user name %s and the password %s (new at each start)\n",
		localuser.DemoUserName, password)
	if err != nil {
		return fmt.Errorf("write the demo sign-in: %w", err)
	}

	return nil
}
