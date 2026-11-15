//go:build windows

package updater

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/windows/registry"
)

// SpyDB.exe in a folder the user can write to, as the installer puts it.
func writableInstallation(t *testing.T) installation {
	return installation{exe: filepath.Join(t.TempDir(), "SpyDB.exe")}
}

// Run from anywhere but where the installer put it (go test's own binary
// here), SpyDB never offers to update itself.
func TestInstalledOnlyFromTheInstaller(t *testing.T) {
	if _, version := installed(); version != "" {
		t.Errorf("a test binary counted as installed SpyDB %s", version)
	}
}

func TestProblemWindows(t *testing.T) {
	if p := writableInstallation(t).problem(); p != "" {
		t.Errorf("a writable folder: %q", p)
	}
	if p := (installation{exe: `Z:\no\such\folder\SpyDB.exe`}).problem(); !strings.Contains(p, "can't change files in") {
		t.Errorf("a folder that isn't there: %q", p)
	}
}

// The update's last step for real: the hidden helper waits for "SpyDB" (a
// stand-in process) to quit, runs the installer silently into a folder with
// a quote and a space in its name, starts SpyDB from it and removes the
// download. It installs SpyDB for this user, so it runs only on a
// throwaway Windows (the release workflow's): SPYDB_INSTALL_TEST=1, with
// SPYDB_SETUP and SPYDB_SETUP_VERSION naming the installer.
func TestHelperInstallsAndRestarts(t *testing.T) {
	setup, version := os.Getenv("SPYDB_SETUP"), os.Getenv("SPYDB_SETUP_VERSION")
	if os.Getenv("SPYDB_INSTALL_TEST") == "" || setup == "" {
		t.Skip("set SPYDB_INSTALL_TEST=1 and SPYDB_SETUP on a throwaway Windows")
	}
	// Not t.TempDir: its cleanup fails the test if a file is still in use.
	base, err := os.MkdirTemp("", "spydb-helper-test-")
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(base, "Anuj's SpyDB")
	exe := filepath.Join(dir, "SpyDB.exe")
	leftover := filepath.Join(base, "spydb-update-test")
	os.MkdirAll(leftover, 0o755)
	data, err := os.ReadFile(setup)
	if err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(leftover, setupName), data, 0o755)
	t.Cleanup(func() {
		exec.Command("taskkill", "/F", "/IM", "SpyDB.exe").Run()
		time.Sleep(2 * time.Second)
		exec.Command(filepath.Join(dir, "uninstall.exe"), "/S", "_?="+dir).Run()
		os.RemoveAll(base)
	})

	// Stands in for SpyDB: quits in about 5 seconds.
	spydb := exec.Command("ping", "-n", "6", "127.0.0.1")
	if err := spydb.Start(); err != nil {
		t.Fatal(err)
	}
	script := helperScript(spydb.Process.Pid, filepath.Join(leftover, setupName), dir, exe, leftover)
	if err := startHelper(script); err != nil {
		t.Fatal(err)
	}
	time.Sleep(3 * time.Second)
	if _, err := os.Stat(exe); err == nil {
		t.Fatal("installed while SpyDB was still running")
	}
	spydb.Wait()

	installed := false
	for start := time.Now(); time.Since(start) < 90*time.Second; time.Sleep(time.Second) {
		_, gone := os.Stat(leftover)
		if _, err := os.Stat(exe); err == nil && os.IsNotExist(gone) {
			installed = true
			break
		}
	}
	if !installed {
		t.Fatalf("not installed in %s (or the download stayed)", dir)
	}
	key, err := registry.OpenKey(registry.CURRENT_USER, uninstallKey, registry.QUERY_VALUE)
	if err != nil {
		t.Fatal(err)
	}
	defer key.Close()
	got, _, _ := key.GetStringValue("DisplayVersion")
	location, _, _ := key.GetStringValue("InstallLocation")
	if got != version || !strings.EqualFold(location, dir) {
		t.Errorf("Settings → Apps says %s in %s, want %s in %s", got, location, version, dir)
	}
	// SpyDB was started again from there.
	for start := time.Now(); ; time.Sleep(time.Second) {
		out, _ := exec.Command("powershell", "-NoProfile", "-Command",
			"(Get-Process -Name SpyDB -ErrorAction SilentlyContinue | Where-Object { $_.Path -eq "+psQuote(exe)+" }).Count").Output()
		if strings.TrimSpace(string(out)) == "1" {
			break
		}
		if time.Since(start) > 30*time.Second {
			t.Fatalf("SpyDB wasn't started again from %s", exe)
		}
	}
}
