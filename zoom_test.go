package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/anujkaushik1/SpyDB/appdata"
)

// A settings folder of the test's own, on macOS and Linux alike.
func zoomHome(t *testing.T) string {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", "")
	path, err := appdata.Path(zoomFile)
	if err != nil {
		t.Fatal(err)
	}
	return path
}

func TestSavedZoom(t *testing.T) {
	path := zoomHome(t)

	if got := savedZoom(); got != 0 {
		t.Fatalf("nothing saved: got %v, want 0 (100%%)", got)
	}
	if err := saveZoom(1.1 * 1.1); err != nil {
		t.Fatal(err)
	}
	if got := savedZoom(); got != 1.21 {
		t.Fatalf("got %v, want 1.21", got)
	}
	data, _ := os.ReadFile(path)
	if string(data) != "{\n  \"zoom\": 1.21\n}" {
		t.Errorf("zoom.json is %q", data)
	}
	if info, err := os.Stat(path); err != nil || info.Mode().Perm() != 0o600 {
		t.Errorf("zoom.json: %v, %v (want readable by you only)", info.Mode().Perm(), err)
	}

	// A damaged or hand-edited file: the window opens at 100%.
	for _, text := range []string{"not json", `{"zoom": 50}`, `{"zoom": 0.1}`, `{"zoom": -1}`, `{}`} {
		if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
			t.Fatal(err)
		}
		if got := savedZoom(); got != 0 {
			t.Errorf("%s: got %v, want 0", text, got)
		}
	}
}

// macOS keeps zooming from its menu bar and doesn't open at a saved zoom.
func TestStartZoom(t *testing.T) {
	zoomHome(t)
	if err := saveZoom(0.9); err != nil {
		t.Fatal(err)
	}
	for goos, want := range map[string]float64{"linux": 0.9, "windows": 0.9, "darwin": 0} {
		if got := startZoom(goos); got != want {
			t.Errorf("%s: got %v, want %v", goos, got, want)
		}
	}
}

// Zooming before the window exists does nothing and writes nothing.
func TestZoomWithoutWindow(t *testing.T) {
	path := zoomHome(t)
	z := &ZoomService{}
	if err := z.ZoomIn(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Clean(path)); !os.IsNotExist(err) {
		t.Errorf("zoom.json written without a window: %v", err)
	}
}
