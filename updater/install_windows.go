//go:build windows

package updater

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"syscall"

	"golang.org/x/sys/windows/registry"
)

const platform = "windows"

const manualCommand = "irm https://raw.githubusercontent.com/anujkaushik1/SpyDB/main/install-windows.ps1 | iex"

// uninstallKey is where SpyDB's installer (build/windows/nsis/project.nsi)
// records it for Settings → Apps, in the user's registry: its version and
// folder.
const uninstallKey = `Software\Microsoft\Windows\CurrentVersion\Uninstall\SpyDB`

// Process creation flags: no console window, not stopped with SpyDB, and
// out of a job SpyDB may run in (some terminals end the job's processes
// when they close).
const (
	createNoWindow         = 0x08000000
	createNewProcessGroup  = 0x00000200
	createBreakawayFromJob = 0x01000000
)

// installation is SpyDB.exe as the installer put it.
type installation struct{ exe string }

// installed finds this copy's version, if the installer put it where it
// runs from: "" for a dev build or a SpyDB.exe run from elsewhere.
func installed() (installation, string) {
	exe, err := os.Executable()
	if err != nil || runtime.GOARCH != "amd64" {
		return installation{}, ""
	}
	if exe, err = filepath.EvalSymlinks(exe); err != nil {
		return installation{}, ""
	}
	key, err := registry.OpenKey(registry.CURRENT_USER, uninstallKey, registry.QUERY_VALUE)
	if err != nil {
		return installation{}, ""
	}
	defer key.Close()
	version, _, err := key.GetStringValue("DisplayVersion")
	if err != nil {
		return installation{}, ""
	}
	location, _, err := key.GetStringValue("InstallLocation")
	if err != nil {
		return installation{}, ""
	}
	// The same folder, however it's spelled (case, short names).
	here, err1 := os.Stat(filepath.Dir(exe))
	there, err2 := os.Stat(location)
	if err1 != nil || err2 != nil || !os.SameFile(here, there) {
		return installation{}, ""
	}
	return installation{exe}, version
}

// powershell is Windows PowerShell, on every Windows 10 and 11.
func powershell() string {
	return filepath.Join(os.Getenv("SystemRoot"), "System32", "WindowsPowerShell", "v1.0", "powershell.exe")
}

// problem says why SpyDB can't update itself, or "". The installer is per
// user, so it needs no administrator, only SpyDB's own folder.
func (i installation) problem() string {
	dir := filepath.Dir(i.exe)
	probe, err := os.CreateTemp(dir, ".spydb-update-*")
	if err != nil {
		return fmt.Sprintf("Your Windows user can't change files in %s.", dir)
	}
	probe.Close()
	os.Remove(probe.Name())
	if _, err := os.Stat(powershell()); err != nil {
		return "SpyDB finishes an update with Windows PowerShell, which isn't here."
	}
	return ""
}

// install downloads release version's installer and checks it. It can't
// run yet: Windows locks SpyDB.exe while SpyDB runs, so relaunch runs it
// once SpyDB has quit. leftover is the folder it's in.
func (i installation) install(ctx context.Context, downloadURL, version string, progress func(Progress)) (leftover string, err error) {
	tmp, err := os.MkdirTemp("", "spydb-update-")
	if err != nil {
		return "", err
	}
	defer func() {
		if err != nil {
			os.RemoveAll(tmp)
		}
	}()
	setup := filepath.Join(tmp, setupName)
	if err := download(ctx, downloadURL+"/v"+version+"/"+setupName, setup, progress); err != nil {
		return "", err
	}

	progress(Progress{Step: "install", Percent: 100})
	data, err := os.ReadFile(setup)
	if err != nil {
		return "", err
	}
	if err := checkSetup(data, version); err != nil {
		return "", err
	}
	return tmp, nil
}

// relaunch starts a hidden PowerShell (helperScript) that waits for SpyDB
// to quit, runs the installer silently into SpyDB's folder, starts SpyDB
// again and removes leftover.
func (i installation) relaunch(leftover string) error {
	return startHelper(helperScript(os.Getpid(), filepath.Join(leftover, setupName), filepath.Dir(i.exe), i.exe, leftover))
}

// startHelper runs script in a hidden Windows PowerShell that outlives SpyDB.
func startHelper(script string) error {
	start := func(flags uint32) error {
		cmd := exec.Command(powershell(), "-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass",
			"-WindowStyle", "Hidden", "-EncodedCommand", encodedCommand(script))
		cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: flags}
		if err := cmd.Start(); err != nil {
			return err
		}
		return cmd.Process.Release()
	}
	flags := uint32(createNoWindow | createNewProcessGroup)
	if err := start(flags | createBreakawayFromJob); err != nil {
		return start(flags) // a job that doesn't allow it
	}
	return nil
}
