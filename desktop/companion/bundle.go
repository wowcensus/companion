package companion

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"time"
)

type Manifest struct {
	SchemaVersion int       `json:"schemaVersion"`
	SourceID      string    `json:"sourceId"`
	Edition       string    `json:"edition"`
	FileName      string    `json:"fileName"`
	SHA256        string    `json:"sha256"`
	Bytes         int64     `json:"bytes"`
	FileModTime   time.Time `json:"fileModTime"`
	CreatedAt     time.Time `json:"createdAt"`
	AppVersion    string    `json:"appVersion"`
}

type Bundle struct {
	Manifest Manifest
	Zip      []byte
	ZipSHA   string
}

func NewManifest(snap *Snapshot, sourceID string) Manifest {
	m := Manifest{
		SchemaVersion: 1,
		SourceID:      sourceID,
		Edition:       snap.File.Edition,
		FileName:      SavedVariablesName,
		SHA256:        snap.SHA256,
		Bytes:         int64(len(snap.Data)),
		FileModTime:   snap.Sig.ModTime.UTC(),
		CreatedAt:     snap.TakenAt.UTC(),
		AppVersion:    AppVersion,
	}
	return m
}

func BuildBundle(snap *Snapshot, m Manifest) (*Bundle, error) {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	mj, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return nil, err
	}
	stamp := m.CreatedAt
	for _, f := range []struct {
		name string
		data []byte
	}{{"manifest.json", mj}, {SavedVariablesName, snap.Data}} {
		w, err := zw.CreateHeader(&zip.FileHeader{Name: f.name, Method: zip.Deflate, Modified: stamp})
		if err != nil {
			return nil, err
		}
		if _, err := w.Write(f.data); err != nil {
			return nil, err
		}
	}
	if err := zw.Close(); err != nil {
		return nil, err
	}
	sum := sha256.Sum256(buf.Bytes())
	return &Bundle{Manifest: m, Zip: buf.Bytes(), ZipSHA: hex.EncodeToString(sum[:])}, nil
}
