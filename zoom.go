package main

import (
	"encoding/json"
	"math"
	"os"

	"github.com/anujkaushik1/SpyDB/appdata"
	"github.com/wailsapp/wails/v3/pkg/application"
)

// ZoomService zooms SpyDB's window on Linux and Windows (Ctrl + / Ctrl - /
// Ctrl 0, see frontend/src/zoomKeys.ts) and remembers the zoom in SpyDB's
// settings folder, so the window opens at it next time (startZoom). macOS
// doesn't use it: there ⌘+ / ⌘- / ⌘0 come from Wails' menu bar, as before.
type ZoomService struct {
	window *application.WebviewWindow // set in main once the window exists
}

// ZoomIn, ZoomOut and ZoomReset (back to 100%) zoom the window, then save
// the zoom it's at now.
func (z *ZoomService) ZoomIn() error {
	return z.zoom((*application.WebviewWindow).ZoomIn)
}

func (z *ZoomService) ZoomOut() error {
	return z.zoom((*application.WebviewWindow).ZoomOut)
}

func (z *ZoomService) ZoomReset() error {
	return z.zoom(func(w *application.WebviewWindow) { w.ZoomReset() })
}

func (z *ZoomService) zoom(change func(*application.WebviewWindow)) error {
	if z.window == nil {
		return nil // no window yet
	}
	change(z.window)
	return saveZoom(z.window.GetZoom())
}

// zoomFile is zoom.json in SpyDB's settings folder: {"zoom": 1.21}.
const zoomFile = "zoom.json"

// The zoom levels a saved file may hold; anything else (a damaged or
// hand-edited file) is ignored and the window opens at 100%.
const minZoom, maxZoom = 0.25, 5.0

// startZoom is the zoom the window opens at: the one saved last time on
// Linux and Windows; 0 (Wails then leaves it at 100%) on macOS, or with
// nothing saved.
func startZoom(goos string) float64 {
	if goos == "darwin" {
		return 0
	}
	return savedZoom()
}

// savedZoom is the zoom in zoom.json, or 0 if there's none to use.
func savedZoom() float64 {
	path, err := appdata.Path(zoomFile)
	if err != nil {
		return 0
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return 0
	}
	var saved struct{ Zoom float64 }
	if json.Unmarshal(data, &saved) != nil || math.IsNaN(saved.Zoom) || saved.Zoom < minZoom || saved.Zoom > maxZoom {
		return 0
	}
	return saved.Zoom
}

func saveZoom(zoom float64) error {
	path, err := appdata.Path(zoomFile)
	if err != nil {
		return err
	}
	// 1.1 × 1.1 is 1.2100000000000002: keep the file readable.
	zoom = math.Round(zoom*10000) / 10000
	return appdata.WritePrivateJSON(path, struct {
		Zoom float64 `json:"zoom"`
	}{zoom})
}
