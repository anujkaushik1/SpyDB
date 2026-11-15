//go:build darwin

package updater

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func writableInstallation(t *testing.T) installation {
	return installation{app: filepath.Join(t.TempDir(), "SpyDB.app")}
}

// fakeApp makes a small app bundle at dir/SpyDB.app, signed ad hoc as the
// release build is, and returns its path.
func fakeApp(t *testing.T, dir, id, version string) string {
	t.Helper()
	app := filepath.Join(dir, "SpyDB.app")
	if err := os.MkdirAll(filepath.Join(app, "Contents", "MacOS"), 0o755); err != nil {
		t.Fatal(err)
	}
	plist := fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0"><dict>
<key>CFBundleExecutable</key><string>spydb</string>
<key>CFBundleIdentifier</key><string>%s</string>
<key>CFBundleShortVersionString</key><string>%s</string>
</dict></plist>`, id, version)
	write(t, filepath.Join(app, "Contents", "Info.plist"), plist)
	write(t, filepath.Join(app, "Contents", "MacOS", "spydb"), "#!/bin/sh\necho "+version+"\n")
	os.Chmod(filepath.Join(app, "Contents", "MacOS", "spydb"), 0o755)
	if out, err := exec.Command("codesign", "--force", "--sign", "-", app).CombinedOutput(); err != nil {
		t.Fatalf("codesign: %v %s", err, out)
	}
	return app
}

func write(t *testing.T, path, text string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
}

// release serves zip as release version's SpyDB-mac.zip; it returns the
// download URL to give install.
func release(t *testing.T, version, zip string) string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v"+version+"/SpyDB-mac.zip" {
			http.NotFound(w, r)
			return
		}
		http.ServeFile(w, r, zip)
	}))
	t.Cleanup(srv.Close)
	return srv.URL
}

// zipOf zips app as the release build does.
func zipOf(t *testing.T, app string) string {
	t.Helper()
	zip := filepath.Join(t.TempDir(), "SpyDB-mac.zip")
	if out, err := exec.Command("ditto", "-c", "-k", "--keepParent", app, zip).CombinedOutput(); err != nil {
		t.Fatalf("ditto: %v %s", err, out)
	}
	return zip
}

// folder lists dir's entries: an update that failed must leave nothing behind.
func folder(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	return names
}

func TestInstallMac(t *testing.T) {
	apps := t.TempDir()
	inst := installation{app: fakeApp(t, apps, bundleID, "0.1.6")}
	url := release(t, "0.1.7", zipOf(t, fakeApp(t, t.TempDir(), bundleID, "0.1.7")))

	var steps []Progress
	leftover, err := inst.install(t.Context(), url, "0.1.7", func(p Progress) { steps = append(steps, p) })
	if err != nil {
		t.Fatal(err)
	}
	if got := plistValue(inst.app, "CFBundleShortVersionString"); got != "0.1.7" {
		t.Errorf("installed version %q", got)
	}
	if err := exec.Command("codesign", "--verify", "--deep", "--strict", inst.app).Run(); err != nil {
		t.Errorf("installed app's signature: %v", err)
	}
	// The old app waits beside it, for relaunch to remove once SpyDB quits.
	if filepath.Dir(leftover) != apps || plistValue(filepath.Join(leftover, "old.app"), "CFBundleShortVersionString") != "0.1.6" {
		t.Errorf("leftover %s: %v", leftover, folder(t, leftover))
	}
	if len(steps) == 0 || steps[0].Step != "download" || !slices.Contains(steps, Progress{"download", 100}) || steps[len(steps)-1] != (Progress{"install", 100}) {
		t.Errorf("progress %v", steps)
	}
}

// A bad download changes nothing: the old app stays, nothing is left over.
func TestInstallMacRefused(t *testing.T) {
	tampered := fakeApp(t, t.TempDir(), bundleID, "0.1.7")
	write(t, filepath.Join(tampered, "Contents", "MacOS", "spydb"), "#!/bin/sh\necho changed after signing\n")
	garbage := filepath.Join(t.TempDir(), "SpyDB-mac.zip")
	write(t, garbage, "not a zip")

	for _, c := range []struct {
		name, zip, want string
	}{
		{"not a zip", garbage, "isn't a SpyDB zip"},
		{"changed after signing", zipOf(t, tampered), "signature"},
		{"another version", zipOf(t, fakeApp(t, t.TempDir(), bundleID, "0.1.5")), "not SpyDB 0.1.7"},
		{"a dev build", zipOf(t, fakeApp(t, t.TempDir(), bundleID+".dev", "0.1.7")), "not SpyDB 0.1.7"},
		{"not on the release", "", "HTTP 404"},
	} {
		t.Run(c.name, func(t *testing.T) {
			apps := t.TempDir()
			inst := installation{app: fakeApp(t, apps, bundleID, "0.1.6")}
			url := release(t, "0.1.7", c.zip)
			if c.zip == "" {
				url = release(t, "0.1.8", garbage)
			}
			_, err := inst.install(t.Context(), url, "0.1.7", func(Progress) {})
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("error %v, want it to mention %q", err, c.want)
			}
			if got := plistValue(inst.app, "CFBundleShortVersionString"); got != "0.1.6" {
				t.Errorf("the app is now %q", got)
			}
			if got := folder(t, apps); !slices.Equal(got, []string{"SpyDB.app"}) {
				t.Errorf("left in the apps folder: %v", got)
			}
		})
	}
}

// A folder the user can't write to: SpyDB says so instead of trying.
func TestInstallMacReadOnlyFolder(t *testing.T) {
	apps := t.TempDir()
	inst := installation{app: fakeApp(t, apps, bundleID, "0.1.6")}
	os.Chmod(apps, 0o555)
	t.Cleanup(func() { os.Chmod(apps, 0o755) })
	if p := inst.problem(); !strings.Contains(p, "can't change apps in") {
		t.Errorf("problem %q", p)
	}
}
