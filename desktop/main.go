package main

import (
	"embed"
	"log"
	"os"
	"os/exec"
	"runtime"
	"sync/atomic"

	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/events"

	"wowcensuscompanion/companion"
)

//go:embed all:frontend/dist
var assets embed.FS

//go:embed trayicon.png
var trayIcon []byte

//go:embed build/appicon.png
var appIcon []byte

func init() {

	application.RegisterEvent[companion.Status]("status")
}

func main() {
	relaunch, installed, err := companion.InstallToUserHome(os.Args[1:])
	if err != nil {
		log.Fatal(err)
	}
	if installed {
		if err := exec.Command(relaunch.Path, relaunch.Args...).Start(); err != nil {
			log.Fatal(err)
		}
		return
	}
	startHidden := false
	for _, a := range os.Args[1:] {
		if a == "--minimized" {
			startHidden = true
		}
	}

	engine, err := companion.NewEngine("")
	if err != nil {
		log.Fatal(err)
	}
	companion.CleanupOldVersion()
	if engine.Config().StartAtLogin {
		if err := companion.SetStartAtLogin(true); err != nil {
			log.Printf("could not update start-at-login entry: %v", err)
		}
	}
	svc := &CompanionService{engine: engine}

	app := application.New(application.Options{
		Name:        "WoWCensus Companion",
		Description: "Watches WoWCensus SavedVariables and uploads them with your consent",
		Icon:        appIcon,
		Services:    []application.Service{application.NewService(svc)},
		Assets:      application.AssetOptions{Handler: application.AssetFileServerFS(assets)},
		Mac: application.MacOptions{

			ApplicationShouldTerminateAfterLastWindowClosed: false,
		},
	})

	win := app.Window.NewWithOptions(application.WebviewWindowOptions{
		Name:             "main",
		Title:            "WoWCensus Companion",
		Width:            980,
		Height:           720,
		MinWidth:         720,
		MinHeight:        520,
		URL:              "/",
		Hidden:           startHidden,
		BackgroundColour: application.NewRGB(17, 19, 24),
	})

	var quitting atomic.Bool
	win.RegisterHook(events.Common.WindowClosing, func(e *application.WindowEvent) {
		if quitting.Load() {
			return
		}
		e.Cancel()
		win.Hide()
	})

	show := func() {
		win.Show()
		win.Restore()
		win.Focus()
	}
	quit := func() {
		quitting.Store(true)
		app.Quit()
	}
	svc.quit = quit
	menu := application.NewMenu()
	menu.Add("Open WoWCensus Companion").OnClick(func(*application.Context) { show() })
	updateItem := menu.Add("Update available").OnClick(func(*application.Context) { show() })
	updateItem.SetHidden(true)
	menu.AddSeparator()
	menu.Add("Quit").OnClick(func(*application.Context) { quit() })
	engine.OnUpdateFound(func(version string) {
		application.InvokeSync(func() {
			updateItem.SetLabel("Update to version " + version)
			updateItem.SetHidden(false)
			menu.Update()
			show()
		})
	})

	tray := app.SystemTray.New()
	if runtime.GOOS == "darwin" {
		tray.SetTemplateIcon(trayIcon)
		tray.SetLabel("WC")
	} else {
		tray.SetIcon(trayIcon)
	}
	tray.SetTooltip("WoWCensus Companion")
	tray.SetMenu(menu)
	tray.OnClick(show)

	if err := app.Run(); err != nil {
		log.Fatal(err)
	}
}
