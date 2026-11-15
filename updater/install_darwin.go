//go:build darwin

package updater

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
)

const platform = "mac"

const manualCommand = "curl -fsSL https://raw.githubusercontent.com/anujkaushik1/SpyDB/main/install-mac.sh | sh"

// bundleID is the released app's; a dev build's is bundleID + ".dev".
const bundleID = "com.anujkaushik.spydb"

// installation is the SpyDB.app this copy runs from.
type installation struct{ app string }

// installed finds the app this copy runs from and its version; "" when it
// isn't a released SpyDB.app (a dev build, or not in an app at all).
func installed() (installation, string) {
	exe, err := os.Executable()
	if err != nil {
		return installation{}, ""
	}
	if exe, err = filepath.EvalSymlinks(exe); err != nil {
		return installation{}, ""
	}
	app := filepath.Dir(filepath.Dir(filepath.Dir(exe))) // …/SpyDB.app/Contents/MacOS/spydb
	if filepath.Ext(app) != ".app" || plistValue(app, "CFBundleIdentifier") != bundleID {
		return installation{}, ""
	}
	return installation{app}, plistValue(app, "CFBundleShortVersionString")
}

func plistValue(app, key string) string {
	out, err := exec.Command("/usr/bin/plutil", "-extract", key, "raw", "-o", "-", filepath.Join(app, "Contents", "Info.plist")).Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// problem says why SpyDB can't replace its app, or "".
func (i installation) problem() string {
	const writable = 2 // W_OK
	if syscall.Access(filepath.Dir(i.app), writable) != nil {
		return fmt.Sprintf("Your Mac user can't change apps in %s.", filepath.Dir(i.app))
	}
	return ""
}

// install puts release version in the app's place. It's unpacked into a
// folder next to the app, so the swap is a rename on the same disk; the
// running app moves into that folder (it keeps running), which relaunch
// removes once it has quit.
func (i installation) install(ctx context.Context, downloadURL, version string, progress func(Progress)) (leftover string, err error) {
	tmp, err := os.MkdirTemp(filepath.Dir(i.app), ".SpyDB-update-")
	if err != nil {
		return "", err
	}
	defer func() {
		if err != nil {
			os.RemoveAll(tmp)
		}
	}()
	zip := filepath.Join(tmp, "SpyDB-mac.zip")
	if err := download(ctx, downloadURL+"/v"+version+"/SpyDB-mac.zip", zip, progress); err != nil {
		return "", err
	}

	progress(Progress{Step: "install", Percent: 100})
	fresh := filepath.Join(tmp, "SpyDB.app")
	if err := exec.CommandContext(ctx, "/usr/bin/ditto", "-x", "-k", zip, tmp).Run(); err != nil {
		return "", errors.New("The download isn't a SpyDB zip. Nothing was changed.")
	}
	if err := exec.CommandContext(ctx, "/usr/bin/codesign", "--verify", "--deep", "--strict", fresh).Run(); err != nil {
		return "", errors.New("The download looks damaged (its signature doesn't check out). Nothing was changed.")
	}
	if id, got := plistValue(fresh, "CFBundleIdentifier"), plistValue(fresh, "CFBundleShortVersionString"); id != bundleID || got != version {
		return "", fmt.Errorf("The download is %s %s, not SpyDB %s. Nothing was changed.", id, got, version)
	}

	old := filepath.Join(tmp, "old.app")
	if err := os.Rename(i.app, old); err != nil {
		return "", fmt.Errorf("Couldn't move the old app aside: %v. Nothing was changed.", err)
	}
	if err := os.Rename(fresh, i.app); err != nil {
		if undo := os.Rename(old, i.app); undo != nil {
			return "", fmt.Errorf("Couldn't put the new app in place (%v), nor the old one back (%v): it's in %s.", err, undo, old)
		}
		return "", fmt.Errorf("Couldn't put the new app in place: %v. Nothing was changed.", err)
	}
	return tmp, nil
}

// relaunch starts a shell that waits (up to a minute) for this process to
// quit, removes leftover and opens the app again.
func (i installation) relaunch(leftover string) error {
	const script = `n=0; while kill -0 "$1" 2>/dev/null && [ $n -lt 300 ]; do sleep 0.2; n=$((n+1)); done; rm -rf "$2"; open "$3"`
	cmd := exec.Command("/bin/sh", "-c", script, "sh", strconv.Itoa(os.Getpid()), leftover, i.app)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true} // not ended with SpyDB
	if err := cmd.Start(); err != nil {
		return err
	}
	return cmd.Process.Release()
}
