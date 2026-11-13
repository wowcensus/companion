package main

import (
	"context"

	"github.com/wailsapp/wails/v3/pkg/application"

	"wowcensuscompanion/companion"
)

type CompanionService struct {
	engine *companion.Engine
	cancel context.CancelFunc
}

type SettingsView struct {
	companion.Config
	Registered bool   `json:"registered"`
	AppDir     string `json:"appDir"`
}

func (s *CompanionService) ServiceStartup(ctx context.Context, _ application.ServiceOptions) error {
	s.engine.OnChange(func(st companion.Status) {
		if app := application.Get(); app != nil {
			app.Event.Emit("status", st)
		}
	})
	runCtx, cancel := context.WithCancel(context.Background())
	s.cancel = cancel
	go s.engine.Run(runCtx)
	return nil
}

func (s *CompanionService) ServiceShutdown() error {
	if s.cancel != nil {
		s.cancel()
	}
	return nil
}

func (s *CompanionService) GetSettings() SettingsView {
	cfg := s.engine.Config()
	view := SettingsView{Config: cfg, Registered: cfg.DeviceToken != "", AppDir: s.engine.AppDir()}
	view.DeviceToken = ""
	view.StartAtLogin = cfg.StartAtLogin && companion.StartAtLoginEnabled()
	return view
}

func (s *CompanionService) SaveSettings(cfg companion.Config) (SettingsView, error) {
	if err := s.engine.UpdateConfig(cfg); err != nil {
		return s.GetSettings(), err
	}
	return s.GetSettings(), nil
}

func (s *CompanionService) GetStatus() companion.Status { return s.engine.Status() }

func (s *CompanionService) DetectInstalls(extraRoots []string) []companion.SavedVarFile {
	return companion.DiscoverWithDefaults(extraRoots)
}

func (s *CompanionService) ScanNow()   { s.engine.ScanNow() }
func (s *CompanionService) UploadNow() { s.engine.UploadNow() }
func (s *CompanionService) BackupNow() { s.engine.BackupNow() }

func (s *CompanionService) MaintenanceReport() companion.MaintenanceReport { return s.engine.Report() }
