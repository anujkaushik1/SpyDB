//go:build !windows

package dynamo

import (
	"os"
	"os/exec"
	"path/filepath"
)

// awsFolder is where the AWS CLI keeps your profiles, as messages name it.
const awsFolder = "~/.aws"

// cliInstallHelp says where to get the AWS CLI, where SpyDB can't install
// its own (Linux; on a Mac it does, see downloadAWSCLI).
const cliInstallHelp = "https://aws.amazon.com/cli/"

// awsCLIPlaces is where an AWS CLI is usually installed. An app opened from
// the Dock or Finder doesn't get your terminal's PATH (no /opt/homebrew/bin),
// so these are checked by name as well as through PATH.
func awsCLIPlaces() []string {
	places := []string{
		"/opt/homebrew/bin/aws", // Homebrew on Apple silicon Macs
		"/usr/local/bin/aws",    // AWS's own Mac installer, Homebrew on Intel Macs, AWS's Linux installer
		"/usr/bin/aws",          // Ubuntu's apt package
		"/snap/bin/aws",         // Ubuntu's snap package
	}
	if home, err := os.UserHomeDir(); err == nil {
		places = append(places, filepath.Join(home, ".local", "bin", "aws")) // AWS's Linux installer, per user
	}
	return places
}

// hideConsole does nothing here: programs started from an app open no window.
func hideConsole(*exec.Cmd) {}
