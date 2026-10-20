// Package devsignin is the seam between the wiring of opendustpan dev and the
// server in pkg/dustpan. The server has no public option for the dev sign-in:
// it registers the maker of the option here, in an internal package that only
// the code of this repository can import, and the wiring of dev asks for the
// option here. An SDK server never gets the option.
package devsignin

import (
	"errors"
	"sync/atomic"

	"opendustpan.org/opendustpan/internal/devcode"
	"opendustpan.org/opendustpan/pkg/identity"
)

// ErrNotRegistered reports a binary where the server did not register the
// maker of the option.
var ErrNotRegistered = errors.New("the server did not register the dev sign-in option")

// Config is what the server needs to serve the dev sign-in. Code is the
// one-time code. Admin is the bootstrap administrator of dev: the person that
// the link signs in gets the role of the site administrator from Admin.
type Config struct {
	Code  *devcode.Code
	Admin identity.Principal
}

// Maker answers the server option for a Config. The option has the type
// dustpan.Option; the seam cannot name it, because pkg/dustpan imports this
// package.
type Maker func(Config) any

// maker holds the registered Maker.
//
//nolint:gochecknoglobals // The seam is one write at start-up and reads after it.
var maker atomic.Pointer[Maker]

// Register sets the maker of the option. pkg/dustpan calls it when its
// package initializes.
func Register(m Maker) { maker.Store(&m) }

// Option answers the server option for c, as a value of the type
// dustpan.Option. It returns ErrNotRegistered when pkg/dustpan is not linked.
func Option(c Config) (any, error) {
	m := maker.Load()
	if m == nil {
		return nil, ErrNotRegistered
	}

	return (*m)(c), nil
}
