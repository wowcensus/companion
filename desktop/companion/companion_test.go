package companion

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func makeInstall(t *testing.T, root, edition, account, content string) string {
	t.Helper()
	dir := filepath.Join(root, "World of Warcraft", edition, "WTF", "Account", account, "SavedVariables")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(dir, SavedVariablesName)
	writeOld(t, p, content)
	return p
}

func writeOld(t *testing.T, p, content string) {
	t.Helper()
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-time.Minute)
	if err := os.Chtimes(p, old, old); err != nil {
		t.Fatal(err)
	}
}

func TestInitialStatusUsesSavedUploadSetting(t *testing.T) {
	dir := t.TempDir()
	cfg := DefaultConfig()
	cfg.AutoUpload = true
	if err := SaveConfig(dir, cfg); err != nil {
		t.Fatal(err)
	}
	e, err := NewEngine(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !e.Status().AutoUpload {
		t.Fatal("initial status should reflect saved automatic uploads")
	}
}

func TestDiscoverFindsEditionsAndAccounts(t *testing.T) {
	root := t.TempDir()
	a := makeInstall(t, root, "_retail_", "ACCOUNT1", "WoWCensusDB = {}")
	b := makeInstall(t, root, "_anniversary_", "ACCOUNT2", "WoWCensusDB = {}")

	_ = os.MkdirAll(filepath.Join(root, "World of Warcraft", "_retail_", "WTF", "Account", "SavedVariables"), 0o755)

	files := Discover([]string{root})
	got := map[string]SavedVarFile{}
	for _, f := range files {
		got[pathKey(f.Path)] = f
	}
	if f, ok := got[pathKey(a)]; !ok || f.Edition != "retail" || f.Account != "ACCOUNT1" {
		t.Fatalf("retail file not found correctly: %+v", files)
	}
	if f, ok := got[pathKey(b)]; !ok || f.Edition != "anniversary" {
		t.Fatalf("anniversary file not found: %+v", files)
	}

	files = Discover([]string{filepath.Join(root, "World of Warcraft", "_retail_")})
	found := false
	for _, f := range files {
		found = found || pathKey(f.Path) == pathKey(a)
	}
	if !found {
		t.Fatal("edition-dir root not detected")
	}
}

func TestSnapshotRejectsFreshAndChangingFiles(t *testing.T) {
	root := t.TempDir()
	p := makeInstall(t, root, "_retail_", "A", "WoWCensusDB = { x = 1 }")
	f := SavedVarFile{Path: p, Edition: "retail"}
	snap, err := TakeSnapshot(f, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if len(snap.SHA256) != 64 || string(snap.Data) != "WoWCensusDB = { x = 1 }" {
		t.Fatalf("bad snapshot %+v", snap)
	}

	if err := os.WriteFile(p, []byte("WoWCensusDB = { x = 2 }"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := TakeSnapshot(f, time.Now()); !errors.Is(err, ErrUnstable) {
		t.Fatalf("want ErrUnstable, got %v", err)
	}
}

func TestBundleContainsManifestAndLua(t *testing.T) {
	snap := &Snapshot{File: SavedVarFile{Edition: "retail", Path: "/secret/path"}, Data: []byte("WoWCensusDB = {}"), SHA256: strings.Repeat("a", 64), TakenAt: time.Now()}
	m := NewManifest(snap, strings.Repeat("b", 64))
	b, err := BuildBundle(snap, m)
	if err != nil {
		t.Fatal(err)
	}
	zr, err := zip.NewReader(bytes.NewReader(b.Zip), int64(len(b.Zip)))
	if err != nil {
		t.Fatal(err)
	}
	names := map[string][]byte{}
	for _, f := range zr.File {
		rc, _ := f.Open()
		names[f.Name], _ = io.ReadAll(rc)
		rc.Close()
	}
	if string(names[SavedVariablesName]) != "WoWCensusDB = {}" {
		t.Fatal("lua missing from bundle")
	}
	if bytes.Contains(names["manifest.json"], []byte("secret")) {
		t.Fatal("manifest leaks the local path")
	}
	if bytes.Contains(names["manifest.json"], []byte(`"system"`)) {
		t.Fatal("system info included without opt-in")
	}
}

func TestStateSourceIDIsSaltedAndPersistent(t *testing.T) {
	dir := t.TempDir()
	s1, err := LoadState(dir)
	if err != nil {
		t.Fatal(err)
	}
	id := s1.SourceID("C:/WoW/x.lua")
	if err := s1.PutReceipt(Receipt{SourceID: id, SHA256: "h", Status: "queued", At: time.Now()}); err != nil {
		t.Fatal(err)
	}
	s2, _ := LoadState(dir)
	if s2.SourceID("C:/WoW/x.lua") != id {
		t.Fatal("salt not persisted")
	}
	if _, ok := s2.Receipt(id, "h"); !ok {
		t.Fatal("receipt not persisted")
	}
	other, _ := LoadState(t.TempDir())
	if other.SourceID("C:/WoW/x.lua") == id {
		t.Fatal("source ID is not salted per install")
	}
}

func TestBackupRotation(t *testing.T) {
	dir := t.TempDir()
	src := strings.Repeat("c", 64)
	base := time.Now().Add(-time.Hour)
	for i := 0; i < 7; i++ {
		data := []byte("WoWCensusDB = {" + strings.Repeat("x", i) + "}")
		snap := &Snapshot{File: SavedVarFile{Edition: "retail"}, Data: data, SHA256: strings.Repeat(string(rune('0'+i)), 64), TakenAt: base.Add(time.Duration(i) * time.Minute)}
		if _, err := WriteBackup(dir, snap, src); err != nil {
			t.Fatal(err)
		}
	}
	n, err := RotateBackups(dir, 3, 1<<30)
	if err != nil || n != 4 {
		t.Fatalf("removed %d, err %v", n, err)
	}
	left, _ := ListBackups(dir)
	if len(left) != 3 {
		t.Fatalf("want 3 left, got %d", len(left))
	}

	if _, err := RotateBackups(dir, 3, 1); err != nil {
		t.Fatal(err)
	}
	left, _ = ListBackups(dir)
	if len(left) != 1 || !strings.Contains(left[0].Path, strings.Repeat("6", 12)) {
		t.Fatalf("cap rotation kept %+v", left)
	}
}

type fakeServer struct {
	puts, completes, registers atomic.Int32
	mu                         sync.Mutex
	token                      string
	failPut                    atomic.Bool
}

func (fs *fakeServer) currentToken() string {
	fs.mu.Lock()
	defer fs.mu.Unlock()
	return fs.token
}

func (fs *fakeServer) rotateToken(tok string) {
	fs.mu.Lock()
	fs.token = tok
	fs.mu.Unlock()
}

func (fs *fakeServer) handler(t *testing.T) http.Handler {
	mux := http.NewServeMux()
	auth := func(w http.ResponseWriter, r *http.Request) bool {
		if r.Header.Get("Authorization") != "Bearer "+fs.currentToken() {
			w.WriteHeader(401)
			_, _ = w.Write([]byte(`{"error":"bad token"}`))
			return false
		}
		return true
	}
	mux.HandleFunc("/v1/devices", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.Header.Get("Authorization") != "" {
			t.Errorf("bad register: %s auth=%q", r.Method, r.Header.Get("Authorization"))
		}
		n := fs.registers.Add(1)
		w.WriteHeader(201)
		_ = json.NewEncoder(w).Encode(map[string]string{"deviceId": "dev_0000000000000000000" + string(rune('0'+n)), "token": fs.currentToken()})
	})
	mux.HandleFunc("/v1/uploads", func(w http.ResponseWriter, r *http.Request) {
		if !auth(w, r) {
			return
		}
		fs.puts.Add(1)
		if fs.failPut.Load() {
			w.Header().Set("Retry-After", "1")
			w.WriteHeader(503)
			return
		}
		body, _ := io.ReadAll(r.Body)
		sum := sha256.Sum256(body)
		if r.Method != http.MethodPut || r.Header.Get("Content-Type") != "application/zip" || len(body) == 0 ||
			r.Header.Get("X-WoWCensus-Zip-Sha256") != hex.EncodeToString(sum[:]) ||
			len(r.Header.Get("X-WoWCensus-Source-Id")) != 64 || len(r.Header.Get("X-WoWCensus-Sha256")) != 64 {
			t.Errorf("bad upload: method %s, %d bytes, headers %v", r.Method, len(body), r.Header)
		}
		fs.completes.Add(1)
		w.WriteHeader(201)
		_ = json.NewEncoder(w).Encode(uploadResponse{Status: "queued", ReceiptID: "00000000-0000-0000-0000-000000000001"})
	})
	return mux
}

func TestEngineUploadsOnceThenOnChange(t *testing.T) {
	root := t.TempDir()
	p := makeInstall(t, root, "_retail_", "A", "WoWCensusDB = { v = 1 }")
	fs := &fakeServer{token: "test-token"}
	var srv *httptest.Server
	srv = httptest.NewServer(fs.handler(t))
	defer srv.Close()

	appDir := t.TempDir()
	e, err := NewEngine(appDir)
	if err != nil {
		t.Fatal(err)
	}
	e.discover = func([]string) []SavedVarFile { return Discover([]string{root}) }
	e.discoverEditions = func([]string) []string { return DiscoverEditions([]string{root}) }
	cfg := e.Config()
	cfg.ServerURL, cfg.AutoUpload, cfg.AutoBackup = srv.URL, true, true
	if err := e.UpdateConfig(cfg); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	e.RunOnce(ctx, false, false)
	if fs.completes.Load() != 1 {
		t.Fatalf("want 1 upload, got %d (log %+v)", fs.completes.Load(), e.Status().Log)
	}
	st := e.Status()
	if len(st.Files) != 1 || !st.Files[0].Uploaded || st.Files[0].LastReceipt.Status != "queued" {
		t.Fatalf("status %+v", st.Files)
	}
	if fs.registers.Load() != 1 || e.Config().DeviceID == "" {
		t.Fatalf("want one automatic registration, got %d", fs.registers.Load())
	}

	e.RunOnce(ctx, false, false)
	e2, _ := NewEngine(appDir)
	e2.discover = e.discover
	e2.discoverEditions = e.discoverEditions
	e2.RunOnce(ctx, false, false)
	if fs.puts.Load() != 1 {
		t.Fatalf("dedup failed: %d uploads", fs.puts.Load())
	}

	writeOld(t, p, "WoWCensusDB = { v = 2 }")
	e2.RunOnce(ctx, false, false)
	if fs.completes.Load() != 2 {
		t.Fatalf("change not uploaded: %d", fs.completes.Load())
	}
	if fs.registers.Load() != 1 {
		t.Fatalf("registered again after restart: %d", fs.registers.Load())
	}

	fs.rotateToken("server-forgot-the-device")
	writeOld(t, p, "WoWCensusDB = { v = 3 }")
	e2.RunOnce(ctx, false, false)
	e2.RunOnce(ctx, true, false)
	if fs.registers.Load() != 2 || fs.completes.Load() != 3 {
		t.Fatalf("did not re-register after 401: registers %d, uploads %d", fs.registers.Load(), fs.completes.Load())
	}
	if bs, _ := ListBackups(e.Config().ResolvedBackupDir(appDir)); len(bs) != 3 {
		t.Fatalf("want 3 backups, got %d", len(bs))
	}
	rep := e2.Report()
	if rep.BackupCount != 3 || len(rep.Files) != 1 {
		t.Fatalf("report %+v", rep)
	}
}

func TestEngineRespectsConsentAndBacksOff(t *testing.T) {
	root := t.TempDir()
	makeInstall(t, root, "_classic_era_", "A", "WoWCensusDB = {}")
	fs := &fakeServer{token: "tok"}
	var srv *httptest.Server
	srv = httptest.NewServer(fs.handler(t))
	defer srv.Close()

	e, _ := NewEngine(t.TempDir())
	e.discover = func([]string) []SavedVarFile { return Discover([]string{root}) }
	e.discoverEditions = func([]string) []string { return DiscoverEditions([]string{root}) }
	cfg := e.Config()
	cfg.ServerURL = srv.URL
	if err := e.UpdateConfig(cfg); err != nil {
		t.Fatal(err)
	}
	e.RunOnce(context.Background(), false, false)
	if fs.puts.Load() != 0 || fs.registers.Load() != 0 {
		t.Fatal("contacted the server without consent")
	}

	fs.failPut.Store(true)
	cfg.AutoUpload = true
	_ = e.UpdateConfig(cfg)
	e.RunOnce(context.Background(), false, false)
	e.RunOnce(context.Background(), false, false)
	if fs.puts.Load() != 1 {
		t.Fatalf("want 1 PUT attempt during backoff, got %d", fs.puts.Load())
	}
	if f := e.Status().Files[0]; f.NextRetry == nil || f.LastError == "" {
		t.Fatalf("backoff not surfaced: %+v", f)
	}

	fs.failPut.Store(false)
	e.RunOnce(context.Background(), true, false)
	if fs.completes.Load() != 1 {
		t.Fatal("upload now did not upload")
	}
}

func TestConfigValidation(t *testing.T) {
	c := DefaultConfig()
	if c.AutoUpload {
		t.Fatal("uploads must default to off")
	}
	c.ServerURL = "http://example.com"
	if c.Validate() == nil {
		t.Fatal("plain http to a remote host must be rejected")
	}
	c.ServerURL = "http://localhost:3000"
	if c.Validate() != nil {
		t.Fatal("localhost http should be allowed")
	}
	c.AutoUpload = true
	if c.Validate() != nil {
		t.Fatal("auto upload must not need a token")
	}
	c.ServerURL = ""
	if c.Validate() == nil {
		t.Fatal("auto upload without a server must be rejected")
	}
	if got := (Config{DeviceToken: "abcdefgh"}).Redacted().DeviceToken; strings.Contains(got, "abcd") {
		t.Fatalf("token not redacted: %s", got)
	}
}

func addonZip(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, body := range files {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = w.Write([]byte(body))
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestAddonInstallAndUpdate(t *testing.T) {
	root := t.TempDir()
	ed := filepath.Join(root, "World of Warcraft", "_classic_era_")
	if err := os.MkdirAll(filepath.Join(ed, "Interface"), 0o755); err != nil {
		t.Fatal(err)
	}
	if eds := DiscoverEditions([]string{root}); len(eds) != 1 || pathKey(eds[0]) != pathKey(ed) {
		t.Fatalf("edition without saves not detected: %v", eds)
	}
	if InstalledAddonVersion(ed) != "" {
		t.Fatal("reported an add-on that is not installed")
	}
	v1 := addonZip(t, map[string]string{"WoWCensus/WoWCensus.toc": "## Title: WoWCensus\n## Version: 0.1.8\n", "WoWCensus/Core.lua": "x=1", "WoWCensus/Population/Old.lua": "old"})
	if err := InstallAddonFiles(ed, v1); err != nil {
		t.Fatal(err)
	}
	if v := InstalledAddonVersion(ed); v != "0.1.8" {
		t.Fatalf("installed version %q", v)
	}
	v2 := addonZip(t, map[string]string{"WoWCensus/WoWCensus.toc": "## Version: 0.1.9\n", "WoWCensus/Core.lua": "x=2"})
	if err := InstallAddonFiles(ed, v2); err != nil {
		t.Fatal(err)
	}
	if v := InstalledAddonVersion(ed); v != "0.1.9" {
		t.Fatalf("updated version %q", v)
	}
	if _, err := os.Stat(filepath.Join(addonDir(ed), "Population", "Old.lua")); !os.IsNotExist(err) {
		t.Fatal("stale file from the old version survived the update")
	}
	entries, _ := os.ReadDir(filepath.Join(ed, "Interface", "AddOns"))
	if len(entries) != 1 {
		t.Fatalf("temporary folders left behind: %v", entries)
	}
}

func TestAddonZipRejectsEscapes(t *testing.T) {
	ed := t.TempDir()
	for _, bad := range []map[string]string{
		{"WoWCensus/WoWCensus.toc": "x", "../evil.lua": "x"},
		{"WoWCensus/WoWCensus.toc": "x", "WoWCensus/../../evil.lua": "x"},
		{"WoWCensus/WoWCensus.toc": "x", "OtherAddon/Core.lua": "x"},
		{"WoWCensus/Core.lua": "no toc"},
	} {
		if err := InstallAddonFiles(ed, addonZip(t, bad)); err == nil {
			t.Fatalf("accepted bad zip %v", bad)
		}
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(ed), "evil.lua")); err == nil {
		t.Fatal("wrote outside the add-on folder")
	}
}

func TestFetchLatestAddon(t *testing.T) {
	zipData := addonZip(t, map[string]string{"WoWCensus/WoWCensus.toc": "## Version: 0.2.0\n"})
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/zip" {
			_, _ = w.Write(zipData)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"tag_name": "v0.2.0", "assets": []map[string]any{
			{"name": "Core.lua", "browser_download_url": srv.URL + "/nope", "size": 1},
			{"name": "WoWCensus-0.2.0.zip", "browser_download_url": srv.URL + "/zip", "size": len(zipData)},
		}})
	}))
	defer srv.Close()
	old := AddonReleaseAPI
	AddonReleaseAPI = srv.URL + "/latest"
	defer func() { AddonReleaseAPI = old }()
	rel, err := FetchLatestAddon(context.Background(), srv.Client())
	if err != nil || rel.Version != "0.2.0" || !strings.HasSuffix(rel.ZipURL, "/zip") {
		t.Fatalf("release %+v err %v", rel, err)
	}
	data, err := DownloadAddon(context.Background(), srv.Client(), rel)
	if err != nil || !bytes.Equal(data, zipData) {
		t.Fatalf("download err %v", err)
	}
}

func TestClientIDIsStableAndSent(t *testing.T) {
	dir := t.TempDir()
	s1, _ := LoadState(dir)
	s2, _ := LoadState(dir)
	id := s1.ClientID()
	if len(id) != 32 || id != s2.ClientID() {
		t.Fatalf("client id %q not stable", id)
	}
	other, _ := LoadState(t.TempDir())
	if other.ClientID() == id {
		t.Fatal("client id is not unique per install")
	}
	got := ""
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.Header.Get("X-WoWCensus-Client-Id")
		w.WriteHeader(201)
		_ = json.NewEncoder(w).Encode(uploadResponse{Status: "queued", ReceiptID: "r"})
	}))
	defer srv.Close()
	snap := &Snapshot{File: SavedVarFile{Edition: "retail"}, Data: []byte("WoWCensusDB = {}"), SHA256: strings.Repeat("a", 64), TakenAt: time.Now()}
	b, _ := BuildBundle(snap, NewManifest(snap, strings.Repeat("b", 64)))
	c := NewClient(srv.URL, "tok")
	c.ClientID = id
	if _, err := c.Upload(context.Background(), b); err != nil || got != id {
		t.Fatalf("client id header %q err %v", got, err)
	}
}

func TestNewerVersion(t *testing.T) {
	for _, c := range []struct {
		a, b string
		want bool
	}{{"0.1.4", "0.1.3", true}, {"v0.2.0", "0.1.9", true}, {"0.1.3", "0.1.3", false}, {"0.1.2", "0.1.3", false}, {"0.1.10", "0.1.9", true}, {"1.0", "0.9.9", true}, {"junk", "0.1.0", false}} {
		if got := NewerVersion(c.a, c.b); got != c.want {
			t.Errorf("NewerVersion(%q, %q) = %v", c.a, c.b, got)
		}
	}
}

func fakeRelease(t *testing.T, version string, payload []byte, sums string) *httptest.Server {
	t.Helper()
	suffix, sumsName := platformAsset()
	asset := "WoWCensus-Companion-" + version + suffix
	if sums == "" {
		h := sha256.Sum256(payload)
		sums = hex.EncodeToString(h[:]) + "  " + asset + "\n"
	}
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/bin":
			_, _ = w.Write(payload)
		case "/sums":
			_, _ = w.Write([]byte(sums))
		default:
			_ = json.NewEncoder(w).Encode(map[string]any{"tag_name": "v" + version, "assets": []map[string]any{
				{"name": asset, "browser_download_url": srv.URL + "/bin", "size": len(payload)},
				{"name": sumsName, "browser_download_url": srv.URL + "/sums", "size": len(sums)},
			}})
		}
	}))
	return srv
}

func TestUpdateDownloadVerifiesChecksum(t *testing.T) {
	payload := []byte("new app binary")
	srv := fakeRelease(t, "9.9.9", payload, "")
	defer srv.Close()
	old := CompanionReleaseAPI
	CompanionReleaseAPI = srv.URL + "/latest"
	defer func() { CompanionReleaseAPI = old }()
	info, err := FetchLatestCompanion(context.Background(), srv.Client())
	if err != nil || info.Version != "9.9.9" {
		t.Fatalf("info %+v err %v", info, err)
	}
	data, err := DownloadUpdate(context.Background(), srv.Client(), info)
	if err != nil || !bytes.Equal(data, payload) {
		t.Fatalf("download err %v", err)
	}
	bad := fakeRelease(t, "9.9.9", payload, strings.Repeat("0", 64)+"  "+info.Asset+"\n")
	defer bad.Close()
	CompanionReleaseAPI = bad.URL + "/latest"
	info, _ = FetchLatestCompanion(context.Background(), bad.Client())
	if _, err := DownloadUpdate(context.Background(), bad.Client(), info); err == nil {
		t.Fatal("accepted an update with a wrong checksum")
	}
}

func TestEnginePreparesUpdateBeforeRestart(t *testing.T) {
	srv := fakeRelease(t, "9.9.9", []byte("new app binary"), "")
	defer srv.Close()
	old := CompanionReleaseAPI
	CompanionReleaseAPI = srv.URL + "/latest"
	defer func() { CompanionReleaseAPI = old }()
	e, err := NewEngine(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	e.httpClient = srv.Client()
	e.CheckForUpdate(context.Background())
	deadline := time.Now().Add(5 * time.Second)
	for !e.Status().Update.Ready && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	st := e.Status().Update
	if !st.Available || !st.Ready || st.Downloading || st.Error != "" {
		t.Fatalf("update not ready for restart: %+v", st)
	}
	e.CheckForUpdate(context.Background())
	if !e.Status().Update.Ready {
		t.Fatal("checking again lost the prepared update")
	}
	if err := e.PrepareUpdate(context.Background()); err != nil {
		t.Fatalf("preparing an already ready update: %v", err)
	}
}

func TestReplaceExecutable(t *testing.T) {
	dir := t.TempDir()
	exe := filepath.Join(dir, "WoWCensus Companion.exe")
	_ = os.WriteFile(exe, []byte("v1"), 0o755)
	if err := ReplaceExecutable(exe, []byte("v2")); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(exe); string(b) != "v2" {
		t.Fatalf("exe holds %q", b)
	}
	if b, _ := os.ReadFile(exe + ".old"); string(b) != "v1" {
		t.Fatal("old version not kept for cleanup")
	}
	old := executablePath
	executablePath = func() (string, error) { return exe, nil }
	defer func() { executablePath = old }()
	CleanupOldVersion()
	if _, err := os.Stat(exe + ".old"); !os.IsNotExist(err) {
		t.Fatal("old version not cleaned up")
	}
}

func TestInstallToUserHome(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows per-user installer path")
	}
	dir := t.TempDir()
	t.Setenv("LOCALAPPDATA", filepath.Join(dir, "Local"))
	source := filepath.Join(dir, "download.exe")
	if err := os.WriteFile(source, []byte("app"), 0o755); err != nil {
		t.Fatal(err)
	}
	old := executablePath
	executablePath = func() (string, error) { return source, nil }
	defer func() { executablePath = old }()
	relaunch, installed, err := InstallToUserHome([]string{"--minimized"})
	if err != nil || !installed {
		t.Fatalf("install: relaunch=%+v installed=%v err=%v", relaunch, installed, err)
	}
	want, _ := UserInstallPath()
	if relaunch.Path != want || len(relaunch.Args) != 1 || relaunch.Args[0] != "--minimized" {
		t.Fatalf("unexpected relaunch: %+v", relaunch)
	}
	if b, err := os.ReadFile(want); err != nil || string(b) != "app" {
		t.Fatalf("installed app: %q %v", b, err)
	}
	executablePath = func() (string, error) { return want, nil }
	if _, installed, err := InstallToUserHome(nil); err != nil || installed {
		t.Fatalf("installed app copied itself again: %v %v", installed, err)
	}
}

func TestReplaceAppBundle(t *testing.T) {
	dir := t.TempDir()
	bundle := filepath.Join(dir, "WoWCensus Companion.app")
	_ = os.MkdirAll(filepath.Join(bundle, "Contents", "MacOS"), 0o755)
	_ = os.WriteFile(filepath.Join(bundle, "Contents", "MacOS", "wowcensus-companion"), []byte("v1"), 0o755)
	z := addonZip(t, map[string]string{
		"WoWCensus Companion.app/Contents/MacOS/wowcensus-companion": "v2",
		"WoWCensus Companion.app/Contents/Info.plist":                "<plist/>",
	})
	if err := ReplaceAppBundle(bundle, z); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(filepath.Join(bundle, "Contents", "MacOS", "wowcensus-companion")); string(b) != "v2" {
		t.Fatalf("bundle holds %q", b)
	}
	if appBundle(filepath.Join(bundle, "Contents", "MacOS", "wowcensus-companion")) != bundle {
		t.Fatal("bundle path not recognised")
	}
	evil := addonZip(t, map[string]string{"../evil": "x"})
	if err := ReplaceAppBundle(bundle, evil); err == nil {
		t.Fatal("accepted an update that writes outside the app")
	}
}

func TestEngineUpdateCheckNotifiesOncePerVersion(t *testing.T) {
	srv := fakeRelease(t, "9.9.9", []byte("x"), "")
	defer srv.Close()
	old := CompanionReleaseAPI
	CompanionReleaseAPI = srv.URL + "/latest"
	defer func() { CompanionReleaseAPI = old }()
	dir := t.TempDir()
	e, _ := NewEngine(dir)
	e.httpClient = srv.Client()
	var found []string
	e.OnUpdateFound(func(v string) { found = append(found, v) })
	e.maybeCheckUpdate(context.Background())
	e.maybeCheckUpdate(context.Background())
	if st := e.Status().Update; !st.Available || st.Version != "9.9.9" {
		t.Fatalf("update status %+v", st)
	}
	e2, _ := NewEngine(dir)
	e2.httpClient = srv.Client()
	e2.OnUpdateFound(func(v string) { found = append(found, v) })
	e2.CheckForUpdate(context.Background())
	if len(found) != 1 {
		t.Fatalf("prompted %d times, want once per version", len(found))
	}
}
