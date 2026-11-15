//go:build linux

package updater

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// SpyDB installs with pkexec, or as root (as these tests run in a container).
func writableInstallation(*testing.T) installation { return installation{} }

func TestDebVersion(t *testing.T) {
	for v, want := range map[string]string{"0.1.4-1": "0.1.4", "0.1.4": "0.1.4", "0.0.0~test-1": "0.0.0~test"} {
		if got := debVersion(v); got != want {
			t.Errorf("debVersion(%q) = %q, want %q", v, got, want)
		}
	}
}

// fakeDeb builds a small spydb package of version (its program prints the
// version) and returns its path. For any architecture, so the test can run
// in an arm64 container too.
func fakeDeb(t *testing.T, version string) string {
	t.Helper()
	root := t.TempDir()
	for path, text := range map[string]string{
		"DEBIAN/control": fmt.Sprintf("Package: spydb\nVersion: %s-1\nArchitecture: all\nMaintainer: test <test@example.com>\nDescription: test\n", version),
		"usr/bin/spydb":  "#!/bin/sh\necho " + version + "\n",
	} {
		os.MkdirAll(filepath.Join(root, filepath.Dir(path)), 0o755)
		if err := os.WriteFile(filepath.Join(root, path), []byte(text), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	deb := filepath.Join(t.TempDir(), "spydb.deb")
	if out, err := exec.Command("dpkg-deb", "--root-owner-group", "--build", root, deb).CombinedOutput(); err != nil {
		t.Fatalf("dpkg-deb: %v %s", err, out)
	}
	return deb
}

func installedVersion(t *testing.T) string {
	t.Helper()
	out, _ := exec.Command("dpkg-query", "-W", "-f", "${Version}", "spydb").Output()
	return string(out)
}

// The .deb update for real: it installs packages as root, so it runs only
// in a throwaway Ubuntu, with SPYDB_UPDATE_DEB_TEST=1.
func TestInstallUbuntu(t *testing.T) {
	if os.Getenv("SPYDB_UPDATE_DEB_TEST") == "" || os.Geteuid() != 0 {
		t.Skip("set SPYDB_UPDATE_DEB_TEST=1 and run as root in a throwaway Ubuntu")
	}
	if out, err := exec.Command("dpkg", "-i", fakeDeb(t, "0.1.6")).CombinedOutput(); err != nil {
		t.Fatalf("dpkg -i: %v %s", err, out)
	}
	serve := map[string]string{} // release file name → local file
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if f, ok := serve[r.URL.Path]; ok {
			http.ServeFile(w, r, f)
			return
		}
		http.NotFound(w, r)
	}))
	t.Cleanup(srv.Close)
	garbage := filepath.Join(t.TempDir(), "garbage.deb")
	os.WriteFile(garbage, []byte("not a package"), 0o644)

	// Refused, and 0.1.6 stays: the wrong version, not a package, not there.
	for _, c := range []struct{ name, file, want string }{
		{"another version", fakeDeb(t, "0.1.5"), "isn't the SpyDB 0.1.7 package"},
		{"not a package", garbage, "isn't the SpyDB 0.1.7 package"},
		{"not on the release", "", "HTTP 404"},
	} {
		serve = map[string]string{}
		if c.file != "" {
			serve["/v0.1.7/spydb_0.1.7_amd64.deb"] = c.file
		}
		_, err := installation{}.install(t.Context(), srv.URL, "0.1.7", func(Progress) {})
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: error %v, want it to mention %q", c.name, err, c.want)
		}
		if v := installedVersion(t); v != "0.1.6-1" {
			t.Fatalf("%s: installed %q", c.name, v)
		}
	}

	serve = map[string]string{"/v0.1.7/spydb_0.1.7_amd64.deb": fakeDeb(t, "0.1.7")}
	var steps []Progress
	if _, err := (installation{}).install(t.Context(), srv.URL, "0.1.7", func(p Progress) { steps = append(steps, p) }); err != nil {
		t.Fatal(err)
	}
	out, _ := exec.Command(program).Output()
	if v := installedVersion(t); v != "0.1.7-1" || strings.TrimSpace(string(out)) != "0.1.7" {
		t.Errorf("installed %q, program says %q", v, out)
	}
	if len(steps) < 2 || steps[0].Step != "download" || steps[len(steps)-1] != (Progress{"install", 100}) {
		t.Errorf("progress %v", steps)
	}
	if left, _ := filepath.Glob(filepath.Join(os.TempDir(), "spydb-update-*")); len(left) > 0 {
		t.Errorf("left behind: %v", left)
	}
}
