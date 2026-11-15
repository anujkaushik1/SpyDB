//go:build linux

package updater

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
)

const platform = "ubuntu"

const manualCommand = "wget -qO- https://raw.githubusercontent.com/anujkaushik1/SpyDB/main/install-ubuntu.sh | sh"

// releaseFile is this platform's file on release version.
func releaseFile(version string) string { return "spydb_" + version + "_amd64.deb" }

// program is where the .deb installs SpyDB (build/linux/nfpm/nfpm.yaml).
const program = "/usr/bin/spydb"

// installation is the spydb package this copy came from.
type installation struct{}

// installed returns the installed package's version, if this copy is the
// package's program: "" for a dev build or a program run from elsewhere.
func installed() (installation, string) {
	exe, err := os.Executable()
	if err != nil || runtime.GOARCH != "amd64" {
		return installation{}, ""
	}
	if exe, err = filepath.EvalSymlinks(exe); err != nil || exe != program {
		return installation{}, ""
	}
	out, err := exec.Command("dpkg-query", "-W", "-f", "${db:Status-Status} ${Version}", "spydb").Output()
	status, version, _ := strings.Cut(strings.TrimSpace(string(out)), " ")
	if err != nil || status != "installed" {
		return installation{}, ""
	}
	return installation{}, debVersion(version)
}

// debVersion drops a package version's revision: 0.1.4-1 is 0.1.4.
func debVersion(v string) string {
	if i := strings.LastIndex(v, "-"); i > 0 {
		return v[:i]
	}
	return v
}

// problem says why SpyDB can't install the package, or "". It asks for the
// password with pkexec, which shows the system's own password window.
func (installation) problem() string {
	if os.Geteuid() == 0 {
		return ""
	}
	if _, err := exec.LookPath("pkexec"); err != nil {
		return "SpyDB asks for your password with pkexec, which isn't installed here."
	}
	return ""
}

// install downloads release version's .deb and installs it with apt, as
// root (through pkexec). The running program keeps running: dpkg puts the
// new file in place of the old one's name.
func (installation) install(ctx context.Context, downloadURL, version string, progress func(Progress)) (leftover string, err error) {
	tmp, err := os.MkdirTemp("", "spydb-update-")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(tmp)
	// Readable by all, so apt's own downloader user can open it.
	if err := os.Chmod(tmp, 0o755); err != nil {
		return "", err
	}
	name := releaseFile(version)
	deb := filepath.Join(tmp, name)
	if err := download(ctx, downloadURL+"/v"+version+"/"+name, deb, progress); err != nil {
		return "", err
	}
	if err := os.Chmod(deb, 0o644); err != nil {
		return "", err
	}
	out, err := exec.CommandContext(ctx, "dpkg-deb", "--show", "--showformat", "${Package} ${Version}", deb).Output()
	if got := strings.TrimSpace(string(out)); err != nil || got != "spydb "+version && !strings.HasPrefix(got, "spydb "+version+"-") {
		return "", fmt.Errorf("The download isn't the SpyDB %s package (%q). Nothing was changed.", version, got)
	}

	progress(Progress{Step: "install", Percent: 100})
	args := []string{"apt-get", "install", "-y", deb}
	if os.Geteuid() != 0 {
		args = append([]string{"pkexec"}, args...)
	}
	// Not cancelled with ctx: dpkg stopped halfway leaves a broken package.
	out, err = exec.Command(args[0], args[1:]...).CombinedOutput()
	var exit *exec.ExitError
	switch {
	case err == nil:
		return "", nil
	case errors.As(err, &exit) && os.Geteuid() != 0 && exit.ExitCode() == 126:
		return "", errors.New("The password window was closed, so nothing was installed.")
	case errors.As(err, &exit) && os.Geteuid() != 0 && exit.ExitCode() == 127:
		return "", errors.New("Your user isn't allowed to install software, so nothing was installed.")
	}
	return "", fmt.Errorf("apt couldn't install it: %s", lastLines(string(out), 3))
}

func lastLines(s string, n int) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	return strings.Join(lines[max(0, len(lines)-n):], " ")
}

// relaunch starts a shell that waits (up to a minute) for this process to
// quit, then starts the new program.
func (installation) relaunch(string) error {
	const script = `n=0; while kill -0 "$1" 2>/dev/null && [ $n -lt 300 ]; do sleep 0.2; n=$((n+1)); done; exec "$2"`
	cmd := exec.Command("/bin/sh", "-c", script, "sh", strconv.Itoa(os.Getpid()), program)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true} // not ended with SpyDB
	if err := cmd.Start(); err != nil {
		return err
	}
	return cmd.Process.Release()
}
