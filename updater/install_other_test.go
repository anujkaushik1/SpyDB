//go:build !darwin && !linux

package updater

import "testing"

func writableInstallation(*testing.T) installation { return installation{} }
