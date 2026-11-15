//go:build !darwin && !linux && !windows

package updater

import "testing"

func writableInstallation(*testing.T) installation { return installation{} }
