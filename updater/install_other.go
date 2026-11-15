//go:build !darwin && !linux && !windows

package updater

import (
	"context"
	"errors"
)

// No releases for other systems: SpyDB never offers an update.
const platform = ""

const manualCommand = ""

type installation struct{}

func installed() (installation, string) { return installation{}, "" }

func (installation) problem() string { return "SpyDB can't update itself here." }

func (installation) install(context.Context, string, string, func(Progress)) (string, error) {
	return "", errors.New("SpyDB can't update itself here.")
}

func (installation) relaunch(string) error { return errors.New("SpyDB can't update itself here.") }
