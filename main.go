package main

import (
	"embed"
	"log"
	"runtime"

	"github.com/anujkaushik1/SpyDB/postgres"
	"github.com/wailsapp/wails/v3/pkg/application"
)

// Wails uses Go's `embed` package to embed the frontend files into the binary.
// Any files in the frontend/dist folder will be embedded into the binary and
// made available to the frontend.
// See https://pkg.go.dev/embed for more information.

//go:embed all:frontend/dist
var assets embed.FS

// refreshTabEvent asks the window to reload the open table tab's rows, as
// its ↻ button does: View → Reload (⌘R) in the macOS menu sends it.
const refreshTabEvent = "spydb:refresh-tab"

func main() {
	zoom := &ZoomService{} // gets the window below

	// 'Services' is a list of Go struct instances. The frontend can call their
	// exported methods through the generated bindings.
	app := application.New(application.Options{
		Name:        "SpyDB",
		Description: "SpyDB",
		Services: []application.Service{
			application.NewService(&postgres.DBService{}),
			application.NewService(zoom),
		},
		Assets: application.AssetOptions{
			Handler: application.AssetFileServerFS(assets),
		},
		Mac: application.MacOptions{
			ApplicationShouldTerminateAfterLastWindowClosed: true,
		},
	})

	setMenu(app) // macOS: ⌘R reloads the open tab's rows, not the window (menu_darwin.go)

	// On macOS the title bar is hidden so the frontend's own header (.titlebar)
	// sits under the traffic lights; InvisibleTitleBarHeight keeps it draggable.
	// Linux and Windows keep the system title bar, and the frontend hides its
	// header there (see main.tsx / style.css).
	zoom.window = app.Window.NewWithOptions(application.WebviewWindowOptions{
		Title:     "SpyDB",
		Width:     1120,
		Height:    720,
		MinWidth:  760, // room for the sidebar plus the editor
		MinHeight: 560,
		Mac: application.MacWindow{
			Appearance:              application.NSAppearanceNameDarkAqua,
			TitleBar:                application.MacTitleBarHiddenInset,
			InvisibleTitleBarHeight: 40,
		},
		BackgroundColour: application.NewRGB(14, 14, 16),
		URL:              "/",
		Zoom:             startZoom(runtime.GOOS), // Linux and Windows: the zoom from last time (see zoom.go)
	})

	// Run the application. This blocks until the application has been exited.
	if err := app.Run(); err != nil {
		log.Fatal(err)
	}
}
