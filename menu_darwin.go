//go:build darwin && !ios && !server

package main

import "github.com/wailsapp/wails/v3/pkg/application"

// setMenu puts up Wails' default macOS menu bar with one change: View →
// Reload (⌘R) asks the window to reload the open table tab's rows
// (refreshTabEvent) instead of reloading the whole page, which closed every
// tab and went back to "Open a table". Force Reload (⇧⌘R), the same page
// reload, is left out. Everything else (zoom, Edit, Window…) is as it was.
func setMenu(app *application.App) {
	menu := application.DefaultApplicationMenu()
	if reload := menu.FindByLabel("Reload"); reload != nil {
		reload.OnClick(func(*application.Context) { app.Event.Emit(refreshTabEvent) })
	}
	if force := menu.FindByLabel("Force Reload"); force != nil {
		menu.RemoveMenuItem(force)
	}
	app.Menu.Set(menu)
}
