package main

import (
	"context"
	"fmt"
	"os/exec"

	"github.com/wailsapp/wails/v3/pkg/application"

	"wowcensuscompanion/companion"
)

type CompanionService struct {
	engine *companion.Engine
	quit   func()
	ctx    context.Context
	cancel context.CancelFunc
}

type Settings struct {
	AutoUpload   bool     `json:"autoUpload"`
	StartAtLogin bool     `json:"startAtLogin"`
	WoWRoots     []string `json:"wowRoots"`
	Registered   bool     `json:"registered"`
}

func (s *CompanionService) ServiceStartup(ctx context.Context, _ application.ServiceOptions) error {
	s.engine.OnChange(func(st companion.Status) {
		if app := application.Get(); app != nil {
			app.Event.Emit("status", st)
		}
	})
	s.ctx, s.cancel = context.WithCancel(context.Background())
	go s.engine.Run(s.ctx)
	go s.engine.CheckAddon(s.ctx)
	return nil
}

func (s *CompanionService) ServiceShutdown() error {
	if s.cancel != nil {
		s.cancel()
	}
	return nil
}

func (s *CompanionService) GetSettings() Settings {
	cfg := s.engine.Config()
	return Settings{
		AutoUpload:   cfg.AutoUpload,
		StartAtLogin: cfg.StartAtLogin && companion.StartAtLoginEnabled(),
		WoWRoots:     cfg.WoWRoots,
		Registered:   cfg.DeviceToken != "",
	}
}

func (s *CompanionService) SaveSettings(in Settings) (Settings, error) {
	cfg := s.engine.Config()
	cfg.AutoUpload = in.AutoUpload
	cfg.StartAtLogin = in.StartAtLogin
	cfg.WoWRoots = in.WoWRoots
	if err := s.engine.UpdateConfig(cfg); err != nil {
		return s.GetSettings(), err
	}
	return s.GetSettings(), nil
}

func (s *CompanionService) GetStatus() companion.Status { return s.engine.Status() }

func (s *CompanionService) DetectInstalls(extraRoots []string) []companion.SavedVarFile {
	return companion.DiscoverWithDefaults(extraRoots)
}

func (s *CompanionService) InstallUpdate() error {
	rl, err := s.engine.InstallUpdate(s.ctx)
	if err != nil {
		return err
	}
	if err := exec.Command(rl.Path, rl.Args...).Start(); err != nil {
		return fmt.Errorf("the update is installed; please reopen the app (%v)", err)
	}
	if s.quit != nil {
		go s.quit()
	}
	return nil
}

func (s *CompanionService) PrepareUpdate() error {
	return s.engine.PrepareUpdate(s.ctx)
}

func (s *CompanionService) CheckForUpdate() companion.UpdateStatus {
	return s.engine.CheckForUpdate(s.ctx)
}

func (s *CompanionService) InstallAddon() (string, error) {
	return s.engine.InstallAddon(s.ctx)
}
