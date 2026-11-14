package main

import (
	"embed"
	"fmt"
	"log"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"sync/atomic"

	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/events"

	"wowcensuscompanion/companion"
)

type trayStrings struct {
	open    string
	update  string
	version string
	quit    string
}

var trayTranslations = map[string]trayStrings{
	"en": {
		open:    "Open WoWCensus Companion",
		update:  "Update available",
		version: "Update to version %s",
		quit:    "Quit",
	},
	"ko": {
		open:    "WoWCensus Companion 열기",
		update:  "업데이트 가능",
		version: "%s 버전으로 업데이트",
		quit:    "종료",
	},
	"es": {
		open:    "Abrir WoWCensus Companion",
		update:  "Actualización disponible",
		version: "Actualizar a la versión %s",
		quit:    "Salir",
	},
	"de": {
		open:    "WoWCensus Companion öffnen",
		update:  "Aktualisierung verfügbar",
		version: "Auf Version %s aktualisieren",
		quit:    "Beenden",
	},
	"fr": {
		open:    "Ouvrir WoWCensus Companion",
		update:  "Mise à jour disponible",
		version: "Mettre à jour vers la version %s",
		quit:    "Quitter",
	},
	"it": {
		open:    "Apri WoWCensus Companion",
		update:  "Aggiornamento disponibile",
		version: "Aggiorna alla versione %s",
		quit:    "Esci",
	},
}

func detectSystemLang() string {
	for _, env := range []string{"LC_ALL", "LC_MESSAGES", "LANG"} {
		if val := strings.ToLower(os.Getenv(env)); val != "" {
			for _, code := range []string{"ko", "es", "de", "fr", "it"} {
				if strings.HasPrefix(val, code) {
					return code
				}
			}
		}
	}
	return "en"
}

func getTrayStrings(lang string) trayStrings {
	if lang == "" || lang == "auto" {
		lang = detectSystemLang()
	}
	if ts, ok := trayTranslations[lang]; ok {
		return ts
	}
	return trayTranslations["en"]
}

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
	curLang := engine.Config().Language
	tStr := getTrayStrings(curLang)
	var latestVersion string

	openItem := menu.Add(tStr.open).OnClick(func(*application.Context) { show() })
	updateItem := menu.Add(tStr.update).OnClick(func(*application.Context) { show() })
	updateItem.SetHidden(true)
	menu.AddSeparator()
	quitItem := menu.Add(tStr.quit).OnClick(func(*application.Context) { quit() })

	updateTrayLabels := func(lang string) {
		curLang = lang
		ts := getTrayStrings(curLang)
		openItem.SetLabel(ts.open)
		if latestVersion != "" {
			updateItem.SetLabel(fmt.Sprintf(ts.version, latestVersion))
		} else {
			updateItem.SetLabel(ts.update)
		}
		quitItem.SetLabel(ts.quit)
		menu.Update()
	}

	svc.onLanguageChange = func(lang string) {
		application.InvokeSync(func() {
			updateTrayLabels(lang)
		})
	}

	engine.OnUpdateFound(func(version string) {
		application.InvokeSync(func() {
			latestVersion = version
			ts := getTrayStrings(curLang)
			updateItem.SetLabel(fmt.Sprintf(ts.version, version))
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
