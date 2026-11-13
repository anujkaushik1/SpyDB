//go:build !darwin || ios || server

package main

import "github.com/wailsapp/wails/v3/pkg/application"

// setMenu: Linux and Windows show no menu bar (Ctrl+R is caught by the
// page, see App.tsx), and the server build has none.
func setMenu(*application.App) {}
