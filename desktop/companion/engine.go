package companion

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"
)

type LogEntry struct {
	Seq     int64     `json:"seq"`
	At      time.Time `json:"at"`
	Level   string    `json:"level"`
	Message string    `json:"message"`
}

type FileStatus struct {
	SavedVarFile
	SourceID      string     `json:"sourceId"`
	Bytes         int64      `json:"bytes"`
	ModTime       time.Time  `json:"modTime"`
	SHA256        string     `json:"sha256"`
	OverThreshold bool       `json:"overThreshold"`
	Uploaded      bool       `json:"uploaded"`
	LastReceipt   *Receipt   `json:"lastReceipt,omitempty"`
	NextRetry     *time.Time `json:"nextRetry,omitempty"`
	LastError     string     `json:"lastError,omitempty"`
}

type Status struct {
	Running    bool         `json:"running"`
	Busy       string       `json:"busy,omitempty"`
	LastScan   time.Time    `json:"lastScan"`
	AutoUpload bool         `json:"autoUpload"`
	Files      []FileStatus `json:"files"`
	Addon      AddonStatus  `json:"addon"`
	Update     UpdateStatus `json:"update"`
	Log        []LogEntry   `json:"log"`
}

type MaintenanceReport struct {
	GeneratedAt time.Time    `json:"generatedAt"`
	ThresholdMB int          `json:"thresholdMB"`
	Files       []FileStatus `json:"files"`
	BackupDir   string       `json:"backupDir"`
	BackupCount int          `json:"backupCount"`
	BackupBytes int64        `json:"backupBytes"`
	BackupCapMB int          `json:"backupCapMB"`
	BackupKeep  int          `json:"backupKeep"`
	Notes       []string     `json:"notes"`
}

type action int

const (
	actScan action = iota
	actUploadNow
	actBackupNow
)

type Engine struct {
	appDir string
	state  *State

	mu       sync.Mutex
	cfg      Config
	status   Status
	onChange func(Status)
	logSeq   int64

	actions chan action

	now              func() time.Time
	newClient        func(serverURL, token string) *Client
	discover         func(roots []string) []SavedVarFile
	discoverEditions func(roots []string) []string
	httpClient       *http.Client

	update        UpdateInfo
	onUpdateFound func(version string)

	addonLatest   AddonRelease
	addonLatestAt time.Time
	addonErr      string
}

const maxLog = 200

func NewEngine(appDir string) (*Engine, error) {
	if appDir == "" {
		d, err := DefaultAppDir()
		if err != nil {
			return nil, err
		}
		appDir = d
	}
	cfg, cfgErr := LoadConfig(appDir)
	st, err := LoadState(appDir)
	if err != nil {
		return nil, err
	}
	e := &Engine{
		appDir: appDir, state: st, cfg: cfg,
		actions:          make(chan action, 8),
		now:              time.Now,
		newClient:        NewClient,
		discover:         DiscoverWithDefaults,
		discoverEditions: DiscoverEditionsWithDefaults,
		httpClient:       &http.Client{Timeout: addonDownloadLimit},
	}
	e.status.Files = []FileStatus{}
	e.status.Addon.Installs = []AddonInstall{}
	e.status.Update.Current = AppVersion
	e.status.Log = []LogEntry{}
	if cfgErr != nil {
		e.logf("error", "%v (defaults loaded)", cfgErr)
	}
	return e, nil
}

func (e *Engine) AppDir() string { return e.appDir }

func (e *Engine) OnChange(fn func(Status)) {
	e.mu.Lock()
	e.onChange = fn
	e.mu.Unlock()
}

func (e *Engine) Config() Config {
	e.mu.Lock()
	defer e.mu.Unlock()
	c := e.cfg
	c.WoWRoots = append([]string{}, e.cfg.WoWRoots...)
	return c
}

func (e *Engine) UpdateConfig(c Config) error {
	old := e.Config()
	c.Normalize()
	c.DeviceID, c.DeviceToken = old.DeviceID, old.DeviceToken
	if c.ServerURL != old.ServerURL {
		c.DeviceID, c.DeviceToken = "", ""
	}
	if err := c.Validate(); err != nil {
		return err
	}
	if c.StartAtLogin != old.StartAtLogin {
		if err := SetStartAtLogin(c.StartAtLogin); err != nil {
			return fmt.Errorf("start at login: %w", err)
		}
	}
	if err := SaveConfig(e.appDir, c); err != nil {
		return err
	}
	if c.ServerURL != old.ServerURL {
		_ = e.state.ClearFailures()
	}
	e.mu.Lock()
	e.cfg = c
	e.status.AutoUpload = c.AutoUpload
	e.mu.Unlock()
	e.logf("info", "Settings saved (automatic uploads %s)", onOff(c.AutoUpload))
	e.Trigger(actScan)
	return nil
}

func (e *Engine) setDevice(id, token string) error {
	c := e.Config()
	c.DeviceID, c.DeviceToken = id, token
	if err := SaveConfig(e.appDir, c); err != nil {
		return err
	}
	e.mu.Lock()
	e.cfg.DeviceID, e.cfg.DeviceToken = id, token
	e.mu.Unlock()
	return nil
}

func (e *Engine) ensureDevice(ctx context.Context) (string, error) {
	cfg := e.Config()
	if cfg.DeviceToken != "" {
		return cfg.DeviceToken, nil
	}
	c := e.newClient(cfg.ServerURL, "")
	c.ClientID = e.state.ClientID()
	id, token, err := c.Register(ctx)
	if err != nil {
		return "", fmt.Errorf("registering this computer: %w", err)
	}
	if err := e.setDevice(id, token); err != nil {
		return "", err
	}
	e.logf("info", "Registered this computer with %s (%s)", cfg.ServerURL, id)
	return token, nil
}

func onOff(b bool) string {
	if b {
		return "on"
	}
	return "off"
}

func (e *Engine) Status() Status {
	e.mu.Lock()
	defer e.mu.Unlock()
	s := e.status
	s.Files = append([]FileStatus{}, e.status.Files...)
	s.Log = append([]LogEntry{}, e.status.Log...)
	return s
}

func (e *Engine) Trigger(a action) {
	select {
	case e.actions <- a:
	default:
	}
}

func (e *Engine) ScanNow()   { e.Trigger(actScan) }
func (e *Engine) UploadNow() { e.Trigger(actUploadNow) }
func (e *Engine) BackupNow() { e.Trigger(actBackupNow) }

func (e *Engine) Run(ctx context.Context) {
	e.setRunning(true)
	defer e.setRunning(false)
	e.logf("info", "Watcher started (polling every %ds)", e.Config().PollSeconds)
	e.maybeCheckUpdate(ctx)
	e.reconcile(ctx, actScan)
	for {
		interval := time.Duration(e.Config().PollSeconds) * time.Second
		timer := time.NewTimer(interval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case a := <-e.actions:
			timer.Stop()
			e.reconcile(ctx, a)
		case <-timer.C:
			e.maybeCheckUpdate(ctx)
			e.reconcile(ctx, actScan)
		}
	}
}

func (e *Engine) RunOnce(ctx context.Context, upload, backup bool) {
	switch {
	case upload:
		e.reconcile(ctx, actUploadNow)
	case backup:
		e.reconcile(ctx, actBackupNow)
	default:
		e.reconcile(ctx, actScan)
	}
}

func (e *Engine) reconcile(ctx context.Context, a action) {
	cfg := e.Config()
	forceUpload := a == actUploadNow
	forceBackup := a == actBackupNow
	switch a {
	case actUploadNow:
		e.setBusy("Uploading")
		if cfg.ServerURL == "" {
			e.logf("error", "Upload now needs a server URL in Settings")
			forceUpload = false
		}
	case actBackupNow:
		e.setBusy("Backing up")
	default:
		e.setBusy("Scanning")
	}
	defer e.setBusy("")

	files := e.discover(cfg.WoWRoots)
	now := e.now()
	statuses := make([]FileStatus, 0, len(files))
	backupDir := cfg.ResolvedBackupDir(e.appDir)
	backedUp := false

	for _, f := range files {
		if ctx.Err() != nil {
			return
		}
		fs := FileStatus{SavedVarFile: f, SourceID: e.state.SourceID(f.Path)}
		sig, err := statSig(f.Path)
		if err != nil {
			fs.LastError = err.Error()
			statuses = append(statuses, fs)
			continue
		}
		fs.Bytes, fs.ModTime = sig.Size, sig.ModTime
		fs.OverThreshold = sig.Size > int64(cfg.SizeThresholdMB)<<20
		seen, known := e.state.SeenSig(f.Path)
		changed := !known || !seen.Sig.Equal(sig)
		if !changed {
			fs.SHA256 = seen.SHA256
		}
		_, hasReceipt := e.state.Receipt(fs.SourceID, fs.SHA256)
		fail, inBackoff := e.state.Backoff(fs.SourceID, fs.SHA256, now)
		wantUpload := (cfg.AutoUpload || forceUpload) && cfg.ServerURL != "" &&
			(changed || !hasReceipt) && (forceUpload || !inBackoff)
		wantBackup := forceBackup || (cfg.AutoBackup && changed)

		if changed || wantUpload || wantBackup {
			snap, err := TakeSnapshot(f, now)
			switch {
			case errors.Is(err, ErrUnstable):
				if forceUpload || forceBackup {
					e.logf("warn", "%s (%s) is still being written; will retry", f.Edition, f.Account)
				}
				fs.LastError = "waiting for the file to settle"
				statuses = append(statuses, fs)
				continue
			case err != nil:
				fs.LastError = err.Error()
				e.logf("error", "Read %s: %v", f.Path, err)
				statuses = append(statuses, fs)
				continue
			}
			fs.SHA256 = snap.SHA256
			if changed {
				_ = e.state.SetSeen(f.Path, snap.Sig, snap.SHA256)
				if known {
					e.logf("info", "New save detected: %s / %s (%s)", f.Edition, f.Account, humanBytes(sig.Size))
				}
			}
			if wantBackup {
				if p, err := WriteBackup(backupDir, snap, fs.SourceID); err != nil {
					e.logf("error", "Backup failed: %v", err)
				} else {
					backedUp = true
					e.logf("info", "Backup written: %s", p)
				}
			}
			_, hasReceipt = e.state.Receipt(fs.SourceID, snap.SHA256)
			fail, inBackoff = e.state.Backoff(fs.SourceID, snap.SHA256, now)
			if wantUpload && !hasReceipt && (forceUpload || !inBackoff) {
				e.upload(ctx, cfg, snap, fs.SourceID)
			} else if forceUpload && hasReceipt {
				e.logf("info", "%s / %s already uploaded (same content)", f.Edition, f.Account)
			}
		}

		if r, ok := e.state.Receipt(fs.SourceID, fs.SHA256); ok {
			fs.Uploaded = true
			fs.LastReceipt = &r
		} else if r, ok := e.state.LatestReceipt(fs.SourceID); ok {
			fs.LastReceipt = &r
		}
		if fail, inBackoff = e.state.Backoff(fs.SourceID, fs.SHA256, e.now()); inBackoff {
			t := fail.NextTry
			fs.NextRetry = &t
			fs.LastError = fail.LastErr
		}
		statuses = append(statuses, fs)
	}

	if backedUp || forceBackup {
		if n, err := RotateBackups(backupDir, cfg.BackupKeep, int64(cfg.BackupCapMB)<<20); err != nil {
			e.logf("error", "Backup rotation: %v", err)
		} else if n > 0 {
			e.logf("info", "Removed %d old backup(s)", n)
		}
	}
	if forceBackup && len(files) == 0 {
		e.logf("warn", "No WoWCensus.lua found to back up")
	}

	if cfg.AutoUpload && cfg.DeviceToken != "" {
		e.refreshReceipts(ctx, cfg, statuses)
	}
	addon := e.addonStatus(cfg)

	e.mu.Lock()
	e.status.Files = statuses
	e.status.Addon = addon
	e.status.LastScan = e.now().UTC()
	e.status.AutoUpload = cfg.AutoUpload
	e.mu.Unlock()
	e.notify()
}

func (e *Engine) upload(ctx context.Context, cfg Config, snap *Snapshot, sourceID string) {
	m := NewManifest(snap, sourceID)
	b, err := BuildBundle(snap, m)
	if err != nil {
		e.logf("error", "Bundle failed: %v", err)
		return
	}
	label := snap.File.Edition + " / " + snap.File.Account
	e.logf("info", "Uploading %s (%s zipped)", label, humanBytes(int64(len(b.Zip))))
	token, err := e.ensureDevice(ctx)
	if err == nil {
		var r Receipt
		c := e.newClient(cfg.ServerURL, token)
		c.ClientID = e.state.ClientID()
		r, err = c.Upload(ctx, b)
		if err == nil {
			_ = e.state.PutReceipt(r)
			if r.Duplicate {
				e.logf("info", "%s: server already has this version", label)
			} else {
				e.logf("info", "%s uploaded. It will appear on wowcensus.io after the next import.", label)
			}
			return
		}
		var apiErr *APIError
		if errors.As(err, &apiErr) && apiErr.Status == http.StatusUnauthorized {
			_ = e.setDevice("", "")
		}
	}
	_ = e.state.RecordFailure(sourceID, snap.SHA256, err, e.now())
	e.logf("error", "Upload of %s failed: %v", label, err)
}

func (e *Engine) Report() MaintenanceReport {
	cfg := e.Config()
	st := e.Status()
	dir := cfg.ResolvedBackupDir(e.appDir)
	rep := MaintenanceReport{
		GeneratedAt: e.now().UTC(), ThresholdMB: cfg.SizeThresholdMB, Files: st.Files,
		BackupDir: dir, BackupCapMB: cfg.BackupCapMB, BackupKeep: cfg.BackupKeep,
	}
	if bs, err := ListBackups(dir); err == nil {
		rep.BackupCount = len(bs)
		for _, b := range bs {
			rep.BackupBytes += b.Bytes
		}
	}
	over := 0
	for _, f := range st.Files {
		if f.OverThreshold {
			over++
		}
	}
	if over > 0 {
		rep.Notes = append(rep.Notes, fmt.Sprintf(
			"%d file(s) exceed %d MB. Large SavedVariables slow WoW's /reload and logout. "+
				"The companion does not trim them: safe compaction needs import acknowledgements from the server and must run while WoW is closed.",
			over, cfg.SizeThresholdMB))
	}
	rep.Notes = append(rep.Notes,
		"Upload receipts say \"queued\": stored and waiting for the ingest worker, not yet imported into the database.",
		"Backups are companion-made gzip copies, rotated by generation count and total size. Your live WoW files are never modified.")
	return rep
}

func (e *Engine) setRunning(b bool) {
	e.mu.Lock()
	e.status.Running = b
	e.mu.Unlock()
	e.notify()
}

func (e *Engine) setBusy(s string) {
	e.mu.Lock()
	e.status.Busy = s
	e.mu.Unlock()
	e.notify()
}

func (e *Engine) logf(level, format string, args ...any) {
	e.mu.Lock()
	e.logSeq++
	e.status.Log = append(e.status.Log, LogEntry{Seq: e.logSeq, At: e.now().UTC(), Level: level, Message: fmt.Sprintf(format, args...)})
	if len(e.status.Log) > maxLog {
		e.status.Log = e.status.Log[len(e.status.Log)-maxLog:]
	}
	e.mu.Unlock()
	e.notify()
}

func (e *Engine) notify() {
	e.mu.Lock()
	fn := e.onChange
	e.mu.Unlock()
	if fn != nil {
		fn(e.Status())
	}
}

func humanBytes(n int64) string {
	switch {
	case n >= 1<<20:
		return fmt.Sprintf("%.1f MB", float64(n)/(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%.1f KB", float64(n)/(1<<10))
	}
	return fmt.Sprintf("%d B", n)
}

const receiptCheckEvery = 10 * time.Minute

func (e *Engine) refreshReceipts(ctx context.Context, cfg Config, files []FileStatus) {
	client := e.newClient(cfg.ServerURL, cfg.DeviceToken)
	for i := range files {
		r := files[i].LastReceipt
		if r == nil || r.ReceiptID == "" || (r.Status != "queued" && r.Status != "stored") || e.now().Sub(r.CheckedAt) < receiptCheckEvery {
			continue
		}
		status, err := client.ReceiptStatus(ctx, r.ReceiptID)
		if err != nil {
			continue
		}
		updated := *r
		updated.Status, updated.CheckedAt = status, e.now().UTC()
		_ = e.state.PutReceipt(updated)
		files[i].LastReceipt = &updated
		if status == "imported" {
			e.logf("info", "%s / %s is now on wowcensus.io", files[i].Edition, files[i].Account)
		}
	}
}

func (e *Engine) addonStatus(cfg Config) AddonStatus {
	installs := AddonInstalls(e.discoverEditions(cfg.WoWRoots))
	e.mu.Lock()
	defer e.mu.Unlock()
	return AddonStatus{Latest: e.addonLatest.Version, Installs: installs, Error: e.addonErr}
}

func (e *Engine) latestAddon(ctx context.Context, force bool) (AddonRelease, error) {
	e.mu.Lock()
	cached, at := e.addonLatest, e.addonLatestAt
	e.mu.Unlock()
	if !force && cached.ZipURL != "" && e.now().Sub(at) < addonLatestMaxAge {
		return cached, nil
	}
	rel, err := FetchLatestAddon(ctx, e.httpClient)
	e.mu.Lock()
	if err != nil {
		e.addonErr = "Couldn't check GitHub for the add-on right now."
	} else {
		e.addonLatest, e.addonLatestAt, e.addonErr = rel, e.now(), ""
	}
	e.mu.Unlock()
	return rel, err
}

func (e *Engine) CheckAddon(ctx context.Context) AddonStatus {
	_, _ = e.latestAddon(ctx, false)
	st := e.addonStatus(e.Config())
	e.mu.Lock()
	e.status.Addon = st
	e.mu.Unlock()
	e.notify()
	return st
}

func (e *Engine) InstallAddon(ctx context.Context) (string, error) {
	editions := e.discoverEditions(e.Config().WoWRoots)
	if len(editions) == 0 {
		return "", errors.New("no World of Warcraft installation found; add your WoW folder in Settings")
	}
	rel, err := e.latestAddon(ctx, true)
	if err != nil {
		return "", fmt.Errorf("couldn't find the add-on download: %w", err)
	}
	data, err := DownloadAddon(ctx, e.httpClient, rel)
	if err != nil {
		return "", err
	}
	var done, failed []string
	for _, ed := range editions {
		label := editionLabel(editionName(ed))
		if err := InstallAddonFiles(ed, data); err != nil {
			failed = append(failed, label)
			e.logf("error", "Add-on install for %s failed: %v", label, err)
			continue
		}
		done = append(done, label)
	}
	e.CheckAddon(ctx)
	if len(done) == 0 {
		return "", fmt.Errorf("the add-on could not be installed (%s). Close World of Warcraft and try again", strings.Join(failed, ", "))
	}
	msg := fmt.Sprintf("Installed WoWCensus %s for %s.", rel.Version, strings.Join(done, ", "))
	if len(failed) > 0 {
		msg += fmt.Sprintf(" Couldn't install for %s; close World of Warcraft and try again.", strings.Join(failed, ", "))
	}
	e.logf("info", "%s Restart World of Warcraft (or /reload) to load it.", msg)
	return msg, nil
}

func editionLabel(key string) string {
	switch key {
	case "retail":
		return "Retail"
	case "classic_era":
		return "Classic Era"
	case "anniversary":
		return "TBC Anniversary"
	case "classic":
		return "MoP Classic"
	case "forever":
		return "Forever"
	case "classic_beta":
		return "Classic Beta"
	case "ptr", "xptr":
		return "PTR"
	case "beta":
		return "Beta"
	}
	return strings.ReplaceAll(key, "_", " ")
}

func (e *Engine) OnUpdateFound(fn func(version string)) {
	e.mu.Lock()
	e.onUpdateFound = fn
	e.mu.Unlock()
}

func (e *Engine) maybeCheckUpdate(ctx context.Context) {
	checked, _ := e.state.UpdateMarks()
	if e.now().Sub(checked) < updateCheckEvery {
		return
	}
	e.CheckForUpdate(ctx)
}

func (e *Engine) CheckForUpdate(ctx context.Context) UpdateStatus {
	info, err := FetchLatestCompanion(ctx, e.httpClient)
	_, seen := e.state.UpdateMarks()
	e.mu.Lock()
	st := UpdateStatus{Current: AppVersion, Installing: e.status.Update.Installing}
	newVersion := ""
	if err == nil && NewerVersion(info.Version, AppVersion) {
		e.update = info
		st.Available, st.Version = true, info.Version
		if info.Version != seen {
			newVersion = info.Version
		}
	}
	e.status.Update = st
	fn := e.onUpdateFound
	e.mu.Unlock()
	if newVersion != "" {
		seen = newVersion
		e.logf("info", "WoWCensus Companion %s is available.", newVersion)
		if fn != nil {
			fn(newVersion)
		}
	}
	_ = e.state.SetUpdateMarks(e.now(), seen)
	e.notify()
	return st
}

func (e *Engine) InstallUpdate(ctx context.Context) (Relaunch, error) {
	e.mu.Lock()
	info := e.update
	if e.status.Update.Installing {
		e.mu.Unlock()
		return Relaunch{}, errors.New("an update is already being installed")
	}
	e.status.Update.Installing, e.status.Update.Error = true, ""
	e.mu.Unlock()
	e.notify()
	fail := func(err error) (Relaunch, error) {
		e.mu.Lock()
		e.status.Update.Installing, e.status.Update.Error = false, err.Error()
		e.mu.Unlock()
		e.logf("error", "Update failed: %v", err)
		return Relaunch{}, err
	}
	if info.URL == "" {
		fresh, err := FetchLatestCompanion(ctx, e.httpClient)
		if err != nil {
			return fail(err)
		}
		info = fresh
	}
	if !NewerVersion(info.Version, AppVersion) {
		return fail(errors.New("you already have the latest version"))
	}
	e.logf("info", "Downloading WoWCensus Companion %s…", info.Version)
	data, err := DownloadUpdate(ctx, e.httpClient, info)
	if err != nil {
		return fail(err)
	}
	rl, err := ApplyUpdate(data)
	if err != nil {
		return fail(err)
	}
	e.logf("info", "Updated to %s. Restarting…", info.Version)
	return rl, nil
}
