//go:build desktop

// Package desktop hosts the Wails frontend for the existing agent session.
package desktop

import (
	"embed"
	"errors"
	"fmt"
	"net/http"
	"os"

	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/events"

	"github.com/zarldev/zarlmono/zarlcode/desktop/session"
	"github.com/zarldev/zarlmono/zarlcode/tui"
)

//go:embed all:frontend/dist
var assets embed.FS

// Run opens the native workbench and joins its live session on shutdown.
func Run() error {
	service := session.New()
	app := application.New(application.Options{
		Name:        "zarlcode workbench",
		Description: "Coding agent workbench",
		ShouldQuit: func() bool {
			if err := service.CheckClose(); err != nil {
				showCloseRecovery(service, err)
				return false
			}
			return true
		},
		OnShutdown: func() {
			if err := service.Shutdown(); err != nil {
				fmt.Fprintf(os.Stderr, "session shutdown: %v\n", err)
			}
		},
		Services: []application.Service{application.NewService(service)},
		Assets: application.AssetOptions{
			Handler: application.AssetFileServerFS(assets),
			Middleware: func(next http.Handler) http.Handler {
				return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; img-src 'self' data:; connect-src 'self'; object-src 'none'; base-uri 'none'; form-action 'none'; frame-src 'none'")
					w.Header().Set("X-Content-Type-Options", "nosniff")
					next.ServeHTTP(w, r)
				})
			},
		},
		Mac: application.MacOptions{ApplicationShouldTerminateAfterLastWindowClosed: true},
	})
	window := app.Window.NewWithOptions(application.WebviewWindowOptions{
		Name: "workbench", Title: "zarlcode", URL: "/",
		Width: 1480, Height: 960, MinWidth: 860, MinHeight: 640,
		BackgroundColour: application.NewRGB(16, 20, 25),
		EnableFileDrop:   false,
		Permissions: map[application.PermissionType]application.Permission{
			application.PermissionCamera:        application.PermissionDeny,
			application.PermissionMicrophone:    application.PermissionDeny,
			application.PermissionGeolocation:   application.PermissionDeny,
			application.PermissionNotifications: application.PermissionDeny,
			application.PermissionClipboardRead: application.PermissionDeny,
		},
	})
	window.RegisterHook(events.Common.WindowClosing, func(event *application.WindowEvent) {
		if err := service.CheckClose(); err != nil {
			event.Cancel()
			showCloseRecovery(service, err)
		}
	})
	if err := app.Run(); err != nil {
		return fmt.Errorf("run desktop workbench: %w", err)
	}
	return nil
}

func showCloseRecovery(service *session.Service, closeErr error) {
	app := application.Get()
	if !errors.Is(closeErr, session.ErrUnsaved) {
		app.Dialog.Warning().SetTitle("Session open").SetMessage(closeErr.Error()).Show()
		return
	}
	dialog := app.Dialog.Warning().SetTitle("Unsaved session").SetMessage("The latest session state is not saved. Export a readable copy before closing, or close without saving. The existing checkpoint will not be overwritten; changes since the last save may be lost.")
	dialog.AddButton("Keep open").SetAsDefault().SetAsCancel()
	dialog.AddButton("Export recovery copy").OnClick(func() {
		if _, err := service.Command(tui.WorkbenchCommand{Action: "export-recovery"}); err != nil {
			app.Dialog.Error().SetMessage(err.Error()).Show()
		}
	})
	dialog.AddButton("Close without saving").OnClick(func() {
		if err := service.ConfirmCloseWithoutSaving(); err != nil {
			app.Dialog.Warning().SetMessage(err.Error()).Show()
			return
		}
		app.Quit()
	})
	dialog.Show()
}
