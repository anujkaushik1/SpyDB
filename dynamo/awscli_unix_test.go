//go:build !windows

package dynamo

import (
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// Homebrew's, AWS's and Ubuntu's places, and AWS's per-user one in your home.
func TestAWSCLIPlacesUnix(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	got := awsCLIPlaces()
	for _, want := range []string{"/opt/homebrew/bin/aws", "/usr/local/bin/aws", "/usr/bin/aws", "/snap/bin/aws", filepath.Join(home, ".local", "bin", "aws")} {
		if !slices.Contains(got, want) {
			t.Errorf("%q missing from %q", want, got)
		}
	}
	if !strings.HasPrefix(awsFolder, "~/") {
		t.Errorf("awsFolder %q", awsFolder)
	}
}
