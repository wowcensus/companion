package companion

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

const (
	combatLogKey      = "combatlog"
	combatLogQuiet    = 10 * time.Minute
	maxCombatLogBytes = 2 << 30
	maxCombatFields   = 43
)

var combatLogNameRe = regexp.MustCompile(`(?i)^WoWCombatLog.*\.txt$`)

func discoverCombatLogs(edDir, edition string) []AddonFile {
	dir := filepath.Join(edDir, "Logs")
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var out []AddonFile
	for _, e := range entries {
		if e.IsDir() || !combatLogNameRe.MatchString(e.Name()) {
			continue
		}
		p := filepath.Join(dir, e.Name())
		if !regularFile(p) {
			continue
		}
		f := AddonFile{SavedVarFile: SavedVarFile{Root: edDir, Edition: edition}, Addon: combatLogKey}
		f.Path = p
		out = append(out, f)
	}
	return out
}

func FilterCombatLog(r io.Reader) ([]byte, int, error) {
	br := bufio.NewReaderSize(r, 1<<20)
	var out bytes.Buffer
	var version string
	events := 0
	for readErr := error(nil); readErr == nil; {
		var line string
		line, readErr = br.ReadString('\n')
		if readErr != nil && readErr != io.EOF {
			return nil, 0, readErr
		}
		line = strings.TrimSuffix(strings.TrimSuffix(line, "\n"), "\r")
		stamp, payload, ok := strings.Cut(line, "  ")
		if !ok {
			continue
		}
		event, _, _ := strings.Cut(payload, ",")
		if event == "COMBAT_LOG_VERSION" {
			version = ""
		} else if version == "" {
			continue
		}
		stamp = strings.TrimPrefix(stamp, "\ufeff")
		var fields [maxCombatFields]string
		f := splitCombatFields(payload, fields[:0])
		if f == nil || !combatTimestampRe.MatchString(stamp) || !filterCombatRecord(f, version) {
			continue
		}
		switch event {
		case "COMBAT_LOG_VERSION":
			version = f[1]
		case "MAP_CHANGE":
		default:
			events++
		}
		out.WriteString(stamp)
		out.WriteString("  ")
		out.WriteString(f[0])
		for _, field := range f[1:] {
			out.WriteByte(',')
			out.WriteString(field)
		}
		out.WriteByte('\n')
	}
	if events == 0 {
		return nil, 0, nil
	}
	return out.Bytes(), events, nil
}

func TakeCombatLogSnapshot(f SavedVarFile, now time.Time) (*Snapshot, error) {
	before, err := statSig(f.Path)
	if err != nil {
		return nil, err
	}
	if before.Size == 0 || now.Sub(before.ModTime) < combatLogQuiet {
		return nil, ErrUnstable
	}
	if before.Size > maxCombatLogBytes {
		return nil, fmt.Errorf("%s is %d MB, above the %d MB limit for combat logs", filepath.Base(f.Path), before.Size>>20, maxCombatLogBytes>>20)
	}
	fh, err := os.Open(f.Path)
	if err != nil {
		return nil, err
	}
	defer fh.Close()
	data, _, err := FilterCombatLog(io.LimitReader(fh, before.Size))
	if err != nil {
		return nil, err
	}
	after, err := statSig(f.Path)
	if err != nil {
		return nil, err
	}
	if !after.Equal(before) {
		return nil, ErrUnstable
	}
	sum := sha256.Sum256(data)
	return &Snapshot{File: f, Data: data, SHA256: hex.EncodeToString(sum[:]), Sig: after, TakenAt: now.UTC()}, nil
}

func combatLogUpload(src []byte, since Cursor) (Delta, error) {
	next := newCursor(since)
	if len(src) == 0 {
		return Delta{Next: next}, nil
	}
	digest := hashText(string(src))
	if next.Hashes["file"] == digest {
		return Delta{Next: next}, nil
	}
	next.Hashes["file"] = digest
	events := bytes.Count(src, []byte("\n"))
	return Delta{Lua: src, Rows: events, Next: next}, nil
}
