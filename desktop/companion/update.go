package companion

import (
	"archive/zip"
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
)

var CompanionReleaseAPI = "https://api.github.com/repos/wowcensus/companion/releases/latest"

var executablePath = func() (string, error) {
	p, err := os.Executable()
	if err != nil {
		return "", err
	}
	return filepath.EvalSymlinks(p)
}

const (
	updateCheckEvery = 24 * time.Hour
	maxUpdateBytes   = 300 << 20
)

type UpdateInfo struct {
	Version string `json:"version"`
	Asset   string `json:"asset"`
	URL     string `json:"-"`
	Size    int64  `json:"size"`
	SumsURL string `json:"-"`
}

type UpdateStatus struct {
	Available   bool   `json:"available"`
	Version     string `json:"version"`
	Current     string `json:"current"`
	Downloading bool   `json:"downloading"`
	Ready       bool   `json:"ready"`
	Installing  bool   `json:"installing"`
	Error       string `json:"error,omitempty"`
}

type Relaunch struct {
	Path string
	Args []string
}

func parseVersion(v string) []int {
	v = strings.TrimPrefix(strings.TrimSpace(v), "v")
	if i := strings.IndexAny(v, "-+"); i >= 0 {
		v = v[:i]
	}
	var out []int
	for _, p := range strings.Split(v, ".") {
		n, err := strconv.Atoi(p)
		if err != nil {
			return nil
		}
		out = append(out, n)
	}
	return out
}

func NewerVersion(candidate, current string) bool {
	a, b := parseVersion(candidate), parseVersion(current)
	if a == nil || b == nil {
		return false
	}
	for i := 0; i < max(len(a), len(b)); i++ {
		x, y := 0, 0
		if i < len(a) {
			x = a[i]
		}
		if i < len(b) {
			y = b[i]
		}
		if x != y {
			return x > y
		}
	}
	return false
}

func platformAsset() (suffix, sums string) {
	switch runtime.GOOS {
	case "windows":
		return "-windows.exe", "SHA256SUMS-windows.txt"
	case "darwin":
		return "-macos.zip", "SHA256SUMS-macos.txt"
	default:
		if os.Getenv("APPIMAGE") != "" {
			return "-linux-x86_64.AppImage", "SHA256SUMS-linux.txt"
		}
		return "-linux-x86_64", "SHA256SUMS-linux.txt"
	}
}

func FetchLatestCompanion(ctx context.Context, client *http.Client) (UpdateInfo, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, CompanionReleaseAPI, nil)
	if err != nil {
		return UpdateInfo{}, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "WoWCensusCompanion/"+AppVersion)
	resp, err := client.Do(req)
	if err != nil {
		return UpdateInfo{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return UpdateInfo{}, fmt.Errorf("update check returned %d", resp.StatusCode)
	}
	var rel struct {
		TagName string `json:"tag_name"`
		Assets  []struct {
			Name string `json:"name"`
			URL  string `json:"browser_download_url"`
			Size int64  `json:"size"`
		} `json:"assets"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&rel); err != nil {
		return UpdateInfo{}, err
	}
	suffix, sums := platformAsset()
	info := UpdateInfo{Version: strings.TrimPrefix(rel.TagName, "v")}
	for _, a := range rel.Assets {
		switch {
		case strings.HasPrefix(a.Name, "WoWCensus-Companion-") && strings.HasSuffix(a.Name, suffix):
			info.Asset, info.URL, info.Size = a.Name, a.URL, a.Size
		case runtime.GOOS == "windows" && a.Name == "wowcensus-companion.exe" && info.URL == "":
			info.Asset, info.URL, info.Size = a.Name, a.URL, a.Size
		case a.Name == sums:
			info.SumsURL = a.URL
		}
	}
	if info.URL == "" {
		return info, fmt.Errorf("version %s has no download for this system yet", info.Version)
	}
	if info.SumsURL == "" {
		return info, fmt.Errorf("version %s has no checksum file", info.Version)
	}
	return info, nil
}

func download(ctx context.Context, client *http.Client, url string, limit int64) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "WoWCensusCompanion/"+AppVersion)
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("download returned %d", resp.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, errors.New("download is unexpectedly large")
	}
	return data, nil
}

func expectedSum(sums []byte, name string) string {
	sc := bufio.NewScanner(bytes.NewReader(sums))
	for sc.Scan() {
		f := strings.Fields(sc.Text())
		if len(f) == 2 && strings.TrimPrefix(f[1], "*") == name {
			return strings.ToLower(f[0])
		}
	}
	return ""
}

func DownloadUpdate(ctx context.Context, client *http.Client, info UpdateInfo) ([]byte, error) {
	sums, err := download(ctx, client, info.SumsURL, 1<<20)
	if err != nil {
		return nil, fmt.Errorf("checksum download failed: %w", err)
	}
	want := expectedSum(sums, info.Asset)
	if want == "" {
		return nil, errors.New("the update has no checksum for this download")
	}
	data, err := download(ctx, client, info.URL, maxUpdateBytes)
	if err != nil {
		return nil, err
	}
	sum := sha256.Sum256(data)
	if hex.EncodeToString(sum[:]) != want {
		return nil, errors.New("the downloaded update is damaged (checksum mismatch); try again later")
	}
	return data, nil
}

func ApplyUpdate(data []byte) (Relaunch, error) {
	exe, err := executablePath()
	if err != nil {
		return Relaunch{}, err
	}
	if runtime.GOOS == "darwin" {
		if bundle := appBundle(exe); bundle != "" {
			if err := ReplaceAppBundle(bundle, data); err != nil {
				return Relaunch{}, err
			}
			return Relaunch{Path: "open", Args: []string{"-n", bundle}}, nil
		}
		return Relaunch{}, errors.New("automatic updates need the app to be in an .app bundle")
	}
	target := exe
	if p := os.Getenv("APPIMAGE"); runtime.GOOS == "linux" && p != "" {
		target = p
	}
	if err := ReplaceExecutable(target, data); err != nil {
		return Relaunch{}, err
	}
	return Relaunch{Path: target}, nil
}

func ReplaceExecutable(target string, data []byte) error {
	old := target + ".old"
	_ = os.Remove(old)
	tmp := target + ".new"
	if err := os.WriteFile(tmp, data, 0o755); err != nil {
		return fmt.Errorf("could not write the update next to the app: %w", err)
	}
	if err := os.Rename(target, old); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("could not replace the app: %w", err)
	}
	if err := os.Rename(tmp, target); err != nil {
		_ = os.Rename(old, target)
		_ = os.Remove(tmp)
		return fmt.Errorf("could not replace the app: %w", err)
	}
	_ = os.Chmod(target, 0o755)
	return nil
}

func appBundle(exe string) string {
	b := filepath.Dir(filepath.Dir(filepath.Dir(exe)))
	if strings.HasSuffix(b, ".app") && filepath.Base(filepath.Dir(exe)) == "MacOS" {
		return b
	}
	return ""
}

func ReplaceAppBundle(bundle string, zipData []byte) error {
	zr, err := zip.NewReader(bytes.NewReader(zipData), int64(len(zipData)))
	if err != nil {
		return fmt.Errorf("the update is unreadable: %w", err)
	}
	stage, err := os.MkdirTemp(filepath.Dir(bundle), ".wowcensus-update-")
	if err != nil {
		return fmt.Errorf("could not write the update next to the app: %w", err)
	}
	defer os.RemoveAll(stage)
	var total int64
	newApp := ""
	for _, f := range zr.File {
		name := filepath.FromSlash(strings.ReplaceAll(f.Name, "\\", "/"))
		dst := filepath.Join(stage, name)
		if !strings.HasPrefix(dst, stage+string(os.PathSeparator)) {
			return fmt.Errorf("the update contains an unexpected file: %s", f.Name)
		}
		first := strings.Split(filepath.ToSlash(name), "/")[0]
		if !strings.HasSuffix(first, ".app") {
			return fmt.Errorf("the update contains an unexpected file: %s", f.Name)
		}
		newApp = filepath.Join(stage, first)
		mode := f.Mode()
		if mode.IsDir() {
			if err := os.MkdirAll(dst, 0o755); err != nil {
				return err
			}
			continue
		}
		if mode&os.ModeSymlink != 0 {
			continue
		}
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return err
		}
		rc, err := f.Open()
		if err != nil {
			return err
		}
		body, err := io.ReadAll(io.LimitReader(rc, maxUpdateBytes-total+1))
		rc.Close()
		if err != nil {
			return err
		}
		total += int64(len(body))
		if total > maxUpdateBytes {
			return errors.New("the update is too large when unpacked")
		}
		perm := mode.Perm()
		if perm == 0 {
			perm = 0o644
		}
		if err := os.WriteFile(dst, body, perm); err != nil {
			return err
		}
	}
	if newApp == "" || !isDir(filepath.Join(newApp, "Contents", "MacOS")) {
		return errors.New("the update does not contain the app")
	}
	old := bundle + ".old"
	_ = os.RemoveAll(old)
	if err := os.Rename(bundle, old); err != nil {
		return fmt.Errorf("could not replace the app: %w", err)
	}
	if err := os.Rename(newApp, bundle); err != nil {
		_ = os.Rename(old, bundle)
		return fmt.Errorf("could not replace the app: %w", err)
	}
	return nil
}

func CleanupOldVersion() {
	exe, err := executablePath()
	if err != nil {
		return
	}
	_ = os.Remove(exe + ".old")
	_ = os.Remove(exe + ".new")
	if p := os.Getenv("APPIMAGE"); p != "" {
		_ = os.Remove(p + ".old")
	}
	if b := appBundle(exe); b != "" {
		_ = os.RemoveAll(b + ".old")
	}
}
