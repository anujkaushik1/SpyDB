package dynamo

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/anujkaushik1/SpyDB/appdata"
)

// AWSCLI is the AWS CLI SpyDB signs in to AWS with: one you installed, or
// SpyDB's own copy in its settings folder.
type AWSCLI struct {
	Path    string `json:"path"`    // the aws program; "" when none was found
	Version string `json:"version"` // e.g. "2.27.6"
	Own     bool   `json:"own"`     // SpyDB's own copy, not one you installed
}

// Where an AWS CLI is usually installed. An app opened from the Dock or
// Finder doesn't get your terminal's PATH (no /opt/homebrew/bin), so these
// are checked by name as well as through PATH.
var awsCLIPlaces = []string{
	"/opt/homebrew/bin/aws", // Homebrew on Apple silicon Macs
	"/usr/local/bin/aws",    // AWS's own Mac installer, Homebrew on Intel Macs, AWS's Linux installer
	"/usr/bin/aws",          // Ubuntu's apt package
	"/snap/bin/aws",         // Ubuntu's snap package
}

// ownAWSCLIPath is where SpyDB keeps its own copy:
// ~/Library/Application Support/SpyDB/aws-cli/bin/aws on macOS,
// ~/.config/SpyDB/aws-cli/bin/aws on Linux.
func ownAWSCLIPath() (string, error) {
	return appdata.Path(filepath.Join("aws-cli", "bin", "aws"))
}

// findAWSCLI returns the AWS CLI to sign in with: yours if you have version
// 2 (version 1 can't sign in through the browser), else SpyDB's own copy;
// Path is "" when there's neither, and SpyDB has to install its copy.
func findAWSCLI(ctx context.Context) AWSCLI {
	var yours []string
	if p, err := exec.LookPath("aws"); err == nil {
		yours = append(yours, p)
	}
	yours = append(yours, awsCLIPlaces...)
	if home, err := os.UserHomeDir(); err == nil {
		yours = append(yours, filepath.Join(home, ".local", "bin", "aws")) // AWS's Linux installer, per user
	}
	own, _ := ownAWSCLIPath()
	return pickAWSCLI(ctx, yours, own)
}

// pickAWSCLI is findAWSCLI for given places: the first of yours that is
// version 2, else own if it is.
func pickAWSCLI(ctx context.Context, yours []string, own string) AWSCLI {
	for _, path := range yours {
		if version, ok := awsCLIVersion(ctx, path); ok {
			return AWSCLI{Path: path, Version: version}
		}
	}
	if own != "" {
		if version, ok := awsCLIVersion(ctx, own); ok {
			return AWSCLI{Path: own, Version: version, Own: true}
		}
	}
	return AWSCLI{}
}

// awsCLIVersion runs `path --version` and returns its version, and whether
// it's a version 2 SpyDB can use. It prints e.g.
// "aws-cli/2.27.6 Python/3.13.3 Darwin/25.5.0 source/arm64".
func awsCLIVersion(ctx context.Context, path string) (string, bool) {
	if info, err := os.Stat(path); err != nil || info.IsDir() {
		return "", false
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, path, "--version").CombinedOutput()
	if err != nil {
		return "", false
	}
	m := awsCLIVersionText.FindSubmatch(out)
	if m == nil {
		return "", false
	}
	major, _ := strconv.Atoi(string(m[1]))
	return string(m[1]) + string(m[2]), major == 2
}

var awsCLIVersionText = regexp.MustCompile(`aws-cli/(\d+)(\.\d+\.\d+)`)

// AWS's installer for Macs, for Apple chips and Intel alike.
const awsCLIMacInstaller = "https://awscli.amazonaws.com/AWSCLIV2.pkg"

// The largest download SpyDB accepts (the installer is about 60 MB).
const maxAWSCLIDownload = 300 << 20

// What `pkgutil --check-signature` says about a genuine installer: Apple
// issued the certificate, Apple's notary service checked the installer, and
// it's signed by Amazon's Apple developer team, 94KV3E626L (as reported for
// AWSCLIV2.pkg in November 2026).
var amazonPkgSignature = []string{
	"Status: signed by a developer certificate issued by Apple for distribution",
	"Notarization: trusted by the Apple notary service",
	"1. Developer ID Installer: AMZN Mobile LLC (94KV3E626L)",
}

// downloadAWSCLI downloads AWS's installer into SpyDB's aws-cli folder and
// checks it's genuinely Amazon's, returning its path; one that fails the
// check is deleted. progress, if given, hears bytes done and the total (-1
// if unknown). It installs nothing.
func downloadAWSCLI(ctx context.Context, progress func(done, total int64)) (string, error) {
	if runtime.GOOS != "darwin" {
		return "", errors.New("SpyDB can't install the AWS CLI on this system yet. Install it yourself (https://aws.amazon.com/cli/), then try again.")
	}
	dir, err := appdata.Path("aws-cli")
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	path := filepath.Join(dir, "AWSCLIV2.pkg")
	if err := download(ctx, awsCLIMacInstaller, path, progress); err != nil {
		return "", err
	}
	if err := checkPkgSignature(ctx, path); err != nil {
		os.Remove(path)
		return "", err
	}
	return path, nil
}

// download saves url as path. It writes path.part and renames it once
// complete, so a cancelled or broken download never looks finished.
func download(ctx context.Context, url, path string, progress func(done, total int64)) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("Couldn't download %s: %w", url, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("Couldn't download %s: the server answered %s.", url, resp.Status)
	}
	if resp.ContentLength > maxAWSCLIDownload {
		return fmt.Errorf("Not downloading %s: it's %d MB, more than expected.", url, resp.ContentLength>>20)
	}
	part := path + ".part"
	f, err := os.OpenFile(part, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer os.Remove(part) // no-op once renamed
	w := &countingWriter{w: f, total: resp.ContentLength, progress: progress}
	n, err := io.Copy(w, io.LimitReader(resp.Body, maxAWSCLIDownload+1))
	if closeErr := f.Close(); err == nil {
		err = closeErr
	}
	switch {
	case err != nil:
		return fmt.Errorf("Couldn't download %s: %w", url, err)
	case n > maxAWSCLIDownload:
		return fmt.Errorf("Stopped downloading %s: it's bigger than expected.", url)
	case resp.ContentLength >= 0 && n != resp.ContentLength:
		return fmt.Errorf("Couldn't download %s: it ended early.", url)
	}
	return os.Rename(part, path)
}

// countingWriter passes writes on and reports progress.
type countingWriter struct {
	w        io.Writer
	done     int64
	total    int64
	progress func(done, total int64)
}

func (c *countingWriter) Write(p []byte) (int, error) {
	n, err := c.w.Write(p)
	c.done += int64(n)
	if c.progress != nil {
		c.progress(c.done, c.total)
	}
	return n, err
}

// checkPkgSignature asks macOS whether the installer at path is signed by
// Amazon and notarized by Apple.
func checkPkgSignature(ctx context.Context, path string) error {
	out, err := exec.CommandContext(ctx, "/usr/sbin/pkgutil", "--check-signature", path).CombinedOutput()
	if err != nil {
		return fmt.Errorf("The downloaded AWS CLI installer isn't properly signed, so SpyDB deleted it. (%s)", strings.TrimSpace(string(out)))
	}
	return pkgSignatureProblem(string(out))
}

// pkgSignatureProblem says why pkgutil's report isn't one of Amazon's
// signed, notarized installer, or nil when it is.
func pkgSignatureProblem(report string) error {
	for _, want := range amazonPkgSignature {
		if !strings.Contains(report, want) {
			return fmt.Errorf("The downloaded AWS CLI installer isn't signed by Amazon (missing %q), so SpyDB deleted it.", want)
		}
	}
	return nil
}
