package main

import (
	"embed"
	"log"
	"os"
	"sync/atomic"

	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/events"

	"wowcensuscompanion/companion"
)

//go:embed all:frontend/dist
var assets embed.FS

//go:embed trayicon.png
var trayIcon []byte

func init() {

	application.RegisterEvent[companion.Status]("status")
}

func main() {
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
	svc := &CompanionService{engine: engine}

	app := application.New(application.Options{
		Name:        "WoWCensus Companion",
		Description: "Watches WoWCensus SavedVariables and uploads them with your consent",
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
	menu := application.NewMenu()
	menu.Add("Open WoWCensus Companion").OnClick(func(*application.Context) { show() })
	menu.AddSeparator()
	menu.Add("Quit").OnClick(func(*application.Context) {
		quitting.Store(true)
		app.Quit()
	})

	tray := app.SystemTray.New()
	tray.SetIcon(trayIcon)
	tray.SetTooltip("WoWCensus Companion")
	tray.SetMenu(menu)
	tray.OnClick(show)

	if err := app.Run(); err != nil {
		log.Fatal(err)
	}
}
