package companion

import (
	"archive/zip"
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

var AddonReleaseAPI = "https://api.github.com/repos/wowcensus/addon/releases/latest"

const (
	addonFolder        = "WoWCensus"
	maxAddonZipBytes   = 20 << 20
	maxAddonUnpacked   = 50 << 20
	addonLatestMaxAge  = time.Hour
	addonDownloadLimit = 2 * time.Minute
)

type AddonInstall struct {
	Edition   string `json:"edition"`
	Dir       string `json:"dir"`
	Installed string `json:"installed"`
}

type AddonStatus struct {
	Latest   string         `json:"latest"`
	Installs []AddonInstall `json:"installs"`
	Error    string         `json:"error,omitempty"`
}

type AddonRelease struct {
	Version string
	ZipURL  string
	Size    int64
}

func DiscoverEditionsWithDefaults(userRoots []string) []string {
	return DiscoverEditions(append(append([]string{}, userRoots...), TypicalInstallRoots()...))
}

func DiscoverEditions(roots []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, root := range roots {
		if !isDir(root) {
			continue
		}
		for _, ed := range findEditionDirs(root) {
			if k := pathKey(ed); !seen[k] {
				seen[k] = true
				out = append(out, ed)
			}
		}
	}
	sort.Strings(out)
	return out
}

func addonDir(editionDir string) string {
	return filepath.Join(editionDir, "Interface", "AddOns", addonFolder)
}

func InstalledAddonVersion(editionDir string) string {
	f, err := os.Open(filepath.Join(addonDir(editionDir), addonFolder+".toc"))
	if err != nil {
		return ""
	}
	defer f.Close()
	sc := bufio.NewScanner(io.LimitReader(f, 64<<10))
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if v, ok := strings.CutPrefix(line, "## Version:"); ok {
			return strings.TrimSpace(v)
		}
	}
	return "unknown"
}

func AddonInstalls(editions []string) []AddonInstall {
	out := make([]AddonInstall, 0, len(editions))
	for _, ed := range editions {
		out = append(out, AddonInstall{Edition: editionName(ed), Dir: ed, Installed: InstalledAddonVersion(ed)})
	}
	return out
}

func FetchLatestAddon(ctx context.Context, client *http.Client) (AddonRelease, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, AddonReleaseAPI, nil)
	if err != nil {
		return AddonRelease{}, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "WoWCensusCompanion/"+AppVersion)
	resp, err := client.Do(req)
	if err != nil {
		return AddonRelease{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return AddonRelease{}, fmt.Errorf("add-on release lookup returned %d", resp.StatusCode)
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
		return AddonRelease{}, err
	}
	for _, a := range rel.Assets {
		if strings.HasPrefix(a.Name, addonFolder) && strings.HasSuffix(strings.ToLower(a.Name), ".zip") {
			return AddonRelease{Version: strings.TrimPrefix(rel.TagName, "v"), ZipURL: a.URL, Size: a.Size}, nil
		}
	}
	return AddonRelease{}, errors.New("the add-on release has no zip file")
}

func DownloadAddon(ctx context.Context, client *http.Client, rel AddonRelease) ([]byte, error) {
	if rel.Size > maxAddonZipBytes {
		return nil, errors.New("add-on download is unexpectedly large")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rel.ZipURL, nil)
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
		return nil, fmt.Errorf("add-on download returned %d", resp.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxAddonZipBytes+1))
	if err != nil {
		return nil, err
	}
	if len(data) > maxAddonZipBytes {
		return nil, errors.New("add-on download is unexpectedly large")
	}
	return data, nil
}

type addonFile struct {
	rel  string
	data []byte
}

func readAddonZip(data []byte) ([]addonFile, error) {
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return nil, fmt.Errorf("add-on zip is unreadable: %w", err)
	}
	var files []addonFile
	var total int64
	hasToc := false
	for _, f := range zr.File {
		name := strings.ReplaceAll(f.Name, "\\", "/")
		if f.FileInfo().IsDir() || strings.HasSuffix(name, "/") {
			continue
		}
		clean := path.Clean(name)
		if path.IsAbs(clean) || strings.Contains(clean, ":") || !strings.HasPrefix(clean, addonFolder+"/") || strings.Contains(clean, "..") {
			return nil, fmt.Errorf("add-on zip has an unexpected file: %s", f.Name)
		}
		rel := strings.TrimPrefix(clean, addonFolder+"/")
		if f.UncompressedSize64 > maxAddonUnpacked {
			return nil, errors.New("add-on zip is too large when unpacked")
		}
		rc, err := f.Open()
		if err != nil {
			return nil, err
		}
		body, err := io.ReadAll(io.LimitReader(rc, maxAddonUnpacked-total+1))
		rc.Close()
		if err != nil {
			return nil, err
		}
		total += int64(len(body))
		if total > maxAddonUnpacked {
			return nil, errors.New("add-on zip is too large when unpacked")
		}
		if rel == addonFolder+".toc" {
			hasToc = true
		}
		files = append(files, addonFile{rel: rel, data: body})
	}
	if !hasToc {
		return nil, errors.New("add-on zip has no WoWCensus.toc")
	}
	return files, nil
}

func InstallAddonFiles(editionDir string, zipData []byte) error {
	files, err := readAddonZip(zipData)
	if err != nil {
		return err
	}
	addons := filepath.Join(editionDir, "Interface", "AddOns")
	if err := os.MkdirAll(addons, 0o755); err != nil {
		return err
	}
	stage, err := os.MkdirTemp(addons, ".WoWCensus-new-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(stage)
	for _, f := range files {
		dst := filepath.Join(stage, filepath.FromSlash(f.rel))
		if !strings.HasPrefix(dst, stage+string(os.PathSeparator)) {
			return fmt.Errorf("refusing to write outside the add-on folder: %s", f.rel)
		}
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(dst, f.data, 0o644); err != nil {
			return err
		}
	}
	target := addonDir(editionDir)
	var old string
	if isDir(target) {
		old = filepath.Join(addons, fmt.Sprintf(".WoWCensus-old-%d", time.Now().UnixNano()))
		if err := os.Rename(target, old); err != nil {
			return fmt.Errorf("could not replace the existing add-on (close World of Warcraft and try again): %w", err)
		}
	}
	if err := os.Rename(stage, target); err != nil {
		if old != "" {
			_ = os.Rename(old, target)
		}
		return err
	}
	if old != "" {
		_ = os.RemoveAll(old)
	}
	return nil
}
