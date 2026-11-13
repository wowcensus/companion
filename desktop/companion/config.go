package companion

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
)

var AppVersion = "0.1.3"

const (
	appDirName          = "WoWCensusCompanion"
	defaultPollSeconds  = 60
	defaultThresholdMB  = 50
	defaultBackupKeep   = 5
	defaultBackupCapMB  = 500
	SavedVariablesName  = "WoWCensus.lua"
	maxLuaBytes         = 256 << 20
	stableQuietDuration = 2
)

type Config struct {
	StartAtLogin bool `json:"startAtLogin"`

	AutoUpload bool `json:"autoUpload"`

	AutoBackup bool `json:"autoBackup"`

	WoWRoots []string `json:"wowRoots"`

	ServerURL   string `json:"serverUrl"`
	DeviceID    string `json:"deviceId"`
	DeviceToken string `json:"deviceToken"`

	SizeThresholdMB int    `json:"sizeThresholdMB"`
	PollSeconds     int    `json:"pollSeconds"`
	BackupDir       string `json:"backupDir"`
	BackupKeep      int    `json:"backupKeep"`
	BackupCapMB     int    `json:"backupCapMB"`
}

func DefaultConfig() Config {
	return Config{
		ServerURL:       "https://api.wowcensus.io",
		SizeThresholdMB: defaultThresholdMB,
		PollSeconds:     defaultPollSeconds,
		BackupKeep:      defaultBackupKeep,
		BackupCapMB:     defaultBackupCapMB,
	}
}

func (c *Config) Normalize() {
	c.PollSeconds = defaultPollSeconds
	if c.SizeThresholdMB <= 0 {
		c.SizeThresholdMB = defaultThresholdMB
	}
	if c.BackupKeep <= 0 {
		c.BackupKeep = defaultBackupKeep
	}
	if c.BackupCapMB <= 0 {
		c.BackupCapMB = defaultBackupCapMB
	}
	c.ServerURL = strings.TrimRight(strings.TrimSpace(c.ServerURL), "/")
	c.DeviceToken = strings.TrimSpace(c.DeviceToken)
	roots := c.WoWRoots[:0]
	seen := map[string]bool{}
	for _, r := range c.WoWRoots {
		r = strings.TrimSpace(r)
		if r == "" {
			continue
		}
		r = filepath.Clean(r)
		if !seen[pathKey(r)] {
			seen[pathKey(r)] = true
			roots = append(roots, r)
		}
	}
	c.WoWRoots = roots
}

func (c Config) Validate() error {
	if c.ServerURL != "" {
		u, err := url.Parse(c.ServerURL)
		if err != nil || u.Host == "" {
			return fmt.Errorf("server URL is not a valid URL")
		}
		local := u.Hostname() == "localhost" || u.Hostname() == "127.0.0.1" || u.Hostname() == "::1"
		if u.Scheme != "https" && !(u.Scheme == "http" && local) {
			return fmt.Errorf("server URL must use https (http is only allowed for localhost)")
		}
	}
	if c.AutoUpload && c.ServerURL == "" {
		return errors.New("automatic uploads need a server URL")
	}
	return nil
}

func (c Config) ResolvedBackupDir(appDir string) string {
	if strings.TrimSpace(c.BackupDir) != "" {
		return filepath.Clean(c.BackupDir)
	}
	return filepath.Join(appDir, "backups")
}

func DefaultAppDir() (string, error) {
	base, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(base, appDirName), nil
}

func LoadConfig(appDir string) (Config, error) {
	cfg := DefaultConfig()
	b, err := os.ReadFile(filepath.Join(appDir, "config.json"))
	if errors.Is(err, os.ErrNotExist) {
		return cfg, nil
	}
	if err != nil {
		return cfg, err
	}
	if err := json.Unmarshal(b, &cfg); err != nil {
		return DefaultConfig(), fmt.Errorf("config.json is unreadable: %w", err)
	}
	cfg.Normalize()
	return cfg, nil
}

func SaveConfig(appDir string, cfg Config) error {
	cfg.Normalize()
	if err := cfg.Validate(); err != nil {
		return err
	}
	b, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	return writeFileAtomic(filepath.Join(appDir, "config.json"), b, 0o600)
}

func (c Config) Redacted() Config {
	if c.DeviceToken != "" {
		c.DeviceToken = "••••" + lastN(c.DeviceToken, 4)
	}
	return c
}

func lastN(s string, n int) string {
	if len(s) <= n {
		return ""
	}
	return s[len(s)-n:]
}
