package dynamo

import (
	"context"
	"encoding/xml"
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
	Older   string `json:"older"`   // when none was new enough: the newest version found anyway, e.g. "2.27.6"
}

// The AWS CLI versions each way of signing in needs.
const (
	anyCLI          = "2.0.0"  // aws sso login, and "who am I" (aws sts get-caller-identity)
	browserLoginCLI = "2.32.0" // aws login: your AWS console sign-in, in the browser
)

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
// ~/Library/Application Support/SpyDB/aws-cli/aws on macOS (AWS's
// installer always makes an aws-cli folder in the folder it's given).
func ownAWSCLIPath() (string, error) {
	return appdata.Path(filepath.Join("aws-cli", "aws"))
}

// findAWSCLI returns the AWS CLI to sign in with, at least version min (see
// anyCLI and browserLoginCLI; version 1 can't sign in through the browser
// at all): yours if it's new enough, else SpyDB's own copy. Path is "" when
// neither is, and SpyDB has to install its copy.
func findAWSCLI(ctx context.Context, min string) AWSCLI {
	var yours []string
	if p, err := exec.LookPath("aws"); err == nil {
		yours = append(yours, p)
	}
	yours = append(yours, awsCLIPlaces...)
	if home, err := os.UserHomeDir(); err == nil {
		yours = append(yours, filepath.Join(home, ".local", "bin", "aws")) // AWS's Linux installer, per user
	}
	own, _ := ownAWSCLIPath()
	return pickAWSCLI(ctx, yours, own, min)
}

// pickAWSCLI is findAWSCLI for given places: the first of yours that is at
// least version min, else own if it is; else the newest version 2 found, as
// Older (to say yours is too old).
func pickAWSCLI(ctx context.Context, yours []string, own, min string) AWSCLI {
	older := ""
	try := func(path string) (string, bool) {
		version, ok := awsCLIVersion(ctx, path)
		if !ok {
			return "", false
		}
		if versionAtLeast(version, min) {
			return version, true
		}
		if older == "" || versionAtLeast(version, older) {
			older = version
		}
		return "", false
	}
	for _, path := range yours {
		if version, ok := try(path); ok {
			return AWSCLI{Path: path, Version: version}
		}
	}
	if own != "" {
		if version, ok := try(own); ok {
			return AWSCLI{Path: own, Version: version, Own: true}
		}
	}
	return AWSCLI{Older: older}
}

// versionAtLeast reports whether version (like "2.27.6") is min or newer,
// comparing each number in turn: 2.32.0 is newer than 2.4.9.
func versionAtLeast(version, min string) bool {
	v, m := strings.Split(version, "."), strings.Split(min, ".")
	for i := range max(len(v), len(m)) {
		a, b := 0, 0
		if i < len(v) {
			a, _ = strconv.Atoi(v[i])
		}
		if i < len(m) {
			b, _ = strconv.Atoi(m[i])
		}
		if a != b {
			return a > b
		}
	}
	return true
}

// awsCLIVersion runs `path --version` and returns its version, and whether
// it's a version 2 SpyDB can use. It prints e.g.
// "aws-cli/2.27.6 Python/3.13.3 Darwin/25.5.0 source/arm64".
func awsCLIVersion(ctx context.Context, path string) (string, bool) {
	version, err := awsCLIVersionWithin(ctx, path, 10*time.Second)
	return version, err == nil
}

// awsCLIVersionWithin is awsCLIVersion with a time limit, saying why it
// can't use the program. A CLI just installed needs longer the first time:
// macOS checks new programs when they first run (seconds, sometimes more).
func awsCLIVersionWithin(ctx context.Context, path string, limit time.Duration) (string, error) {
	if info, err := os.Stat(path); err != nil || info.IsDir() {
		return "", fmt.Errorf("there's no program at %s", path)
	}
	ctx, cancel := context.WithTimeout(ctx, limit)
	defer cancel()
	out, err := exec.CommandContext(ctx, path, "--version").CombinedOutput()
	if ctx.Err() != nil {
		return "", fmt.Errorf("it didn't answer within %s", limit)
	}
	if err != nil {
		return "", fmt.Errorf("%v: %s", err, lastLine(out))
	}
	m := awsCLIVersionText.FindSubmatch(out)
	if m == nil {
		return "", fmt.Errorf("it printed %q, not an AWS CLI version", lastLine(out))
	}
	if string(m[1]) != "2" {
		return string(m[1]) + string(m[2]), fmt.Errorf("it's version %s%s; SpyDB needs version 2", m[1], m[2])
	}
	return string(m[1]) + string(m[2]), nil
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

// downloadAWSCLI downloads AWS's installer into SpyDB's downloads folder and
// checks it's genuinely Amazon's, returning its path; one that fails the
// check is deleted. progress, if given, hears bytes done and the total (-1
// if unknown). It installs nothing (see installAWSCLI).
func downloadAWSCLI(ctx context.Context, progress func(done, total int64)) (string, error) {
	if runtime.GOOS != "darwin" {
		return "", errors.New("SpyDB can't install the AWS CLI on this system yet. Install it yourself (https://aws.amazon.com/cli/), then try again.")
	}
	dir, err := appdata.Path("downloads")
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

// installAWSCLI installs AWS's installer at pkg into SpyDB's settings folder,
// just for you (no administrator password), then deletes the installer. It
// should come from downloadAWSCLI, which checks it's Amazon's. The CLI lands
// in an aws-cli folder there (see ownAWSCLIPath); a copy already there is
// replaced. It's AWS's documented "Command line - Current user" install:
//
//	installer -pkg AWSCLIV2.pkg -target CurrentUserHomeDirectory -applyChoiceChangesXML choices.xml
func installAWSCLI(ctx context.Context, pkg string) (AWSCLI, error) {
	defer os.Remove(pkg)
	if runtime.GOOS != "darwin" {
		return AWSCLI{}, errors.New("SpyDB can't install the AWS CLI on this system yet. Install it yourself (https://aws.amazon.com/cli/), then try again.")
	}
	own, err := ownAWSCLIPath()
	if err != nil {
		return AWSCLI{}, err
	}
	folder := filepath.Dir(filepath.Dir(own))          // SpyDB's settings folder; the installer adds aws-cli/
	if err := os.MkdirAll(folder, 0o700); err != nil { // AWS: "The folder must already exist"
		return AWSCLI{}, err
	}
	tmp, err := os.MkdirTemp("", "spydb-aws-cli-")
	if err != nil {
		return AWSCLI{}, err
	}
	defer os.RemoveAll(tmp)
	choices := filepath.Join(tmp, "choices.xml")
	if err := os.WriteFile(choices, []byte(installChoices(folder)), 0o600); err != nil {
		return AWSCLI{}, err
	}
	out, err := exec.CommandContext(ctx, "/usr/sbin/installer", "-pkg", pkg,
		"-target", "CurrentUserHomeDirectory", "-applyChoiceChangesXML", choices).CombinedOutput()
	if err != nil {
		if ctx.Err() != nil {
			return AWSCLI{}, errors.New("Installing the AWS CLI was cancelled.")
		}
		return AWSCLI{}, fmt.Errorf("Couldn't install the AWS CLI: %s", lastLine(out))
	}
	version, err := awsCLIVersionWithin(ctx, own, 2*time.Minute)
	if err != nil {
		return AWSCLI{}, fmt.Errorf("The AWS CLI installed, but SpyDB couldn't run it: %v", err)
	}
	return AWSCLI{Path: own, Version: version, Own: true}, nil
}

// installChoices is AWS's choices.xml for a current-user install into
// folder (verbatim from AWS's guide, with folder in place of /Users/myusername).
func installChoices(folder string) string {
	var escaped strings.Builder
	xml.EscapeText(&escaped, []byte(folder))
	return `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
  <array>
    <dict>
      <key>choiceAttribute</key>
      <string>customLocation</string>
      <key>attributeSetting</key>
      <string>` + escaped.String() + `</string>
      <key>choiceIdentifier</key>
      <string>default</string>
    </dict>
  </array>
</plist>
`
}
