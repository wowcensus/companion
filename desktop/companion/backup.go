package companion

import (
	"compress/gzip"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

type BackupInfo struct {
	Path     string    `json:"path"`
	SourceID string    `json:"sourceId"`
	Bytes    int64     `json:"bytes"`
	At       time.Time `json:"at"`
}

func WriteBackup(dir string, snap *Snapshot, sourceID string) (string, error) {
	sub := filepath.Join(dir, sourceID[:16])
	if err := os.MkdirAll(sub, 0o700); err != nil {
		return "", err
	}
	if existing, _ := filepath.Glob(filepath.Join(sub, "*-"+snap.SHA256[:12]+".lua.gz")); len(existing) > 0 {
		return existing[0], nil
	}
	name := fmt.Sprintf("%s-%s.lua.gz", snap.TakenAt.UTC().Format("20060102T150405Z"), snap.SHA256[:12])
	final := filepath.Join(sub, name)
	tmp, err := os.CreateTemp(sub, ".backup-*")
	if err != nil {
		return "", err
	}
	tmpName := tmp.Name()
	zw, _ := gzip.NewWriterLevel(tmp, gzip.BestCompression)
	zw.Name = SavedVariablesName
	zw.ModTime = snap.Sig.ModTime
	_, werr := zw.Write(snap.Data)
	cerr := zw.Close()
	serr := tmp.Sync()
	clerr := tmp.Close()
	for _, e := range []error{werr, cerr, serr, clerr} {
		if e != nil {
			_ = os.Remove(tmpName)
			return "", e
		}
	}
	if err := os.Rename(tmpName, final); err != nil {
		_ = os.Remove(tmpName)
		return "", err
	}

	_ = os.WriteFile(filepath.Join(sub, "edition.txt"), []byte(snap.File.Edition+"\n"), 0o600)
	return final, nil
}

func ListBackups(dir string) ([]BackupInfo, error) {
	var out []BackupInfo
	subs, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	for _, s := range subs {
		if !s.IsDir() {
			continue
		}
		files, _ := os.ReadDir(filepath.Join(dir, s.Name()))
		for _, f := range files {
			if f.IsDir() || !strings.HasSuffix(f.Name(), ".lua.gz") {
				continue
			}
			fi, err := f.Info()
			if err != nil {
				continue
			}
			at := fi.ModTime()
			if ts, err := time.Parse("20060102T150405Z", strings.SplitN(f.Name(), "-", 2)[0]); err == nil {
				at = ts
			}
			out = append(out, BackupInfo{Path: filepath.Join(dir, s.Name(), f.Name()), SourceID: s.Name(), Bytes: fi.Size(), At: at})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].At.After(out[j].At) })
	return out, nil
}

func RotateBackups(dir string, keep int, capBytes int64) (removed int, err error) {
	all, err := ListBackups(dir)
	if err != nil {
		return 0, err
	}
	perSource := map[string]int{}
	var kept []BackupInfo
	var total int64
	for _, b := range all {
		perSource[b.SourceID]++
		if perSource[b.SourceID] > keep {
			if os.Remove(b.Path) == nil {
				removed++
			}
			continue
		}
		kept = append(kept, b)
		total += b.Bytes
	}

	newest := map[string]string{}
	for _, b := range kept {
		if _, ok := newest[b.SourceID]; !ok {
			newest[b.SourceID] = b.Path
		}
	}
	for i := len(kept) - 1; i >= 0 && total > capBytes; i-- {
		b := kept[i]
		if newest[b.SourceID] == b.Path {
			continue
		}
		if os.Remove(b.Path) == nil {
			removed++
			total -= b.Bytes
		}
	}
	return removed, nil
}
