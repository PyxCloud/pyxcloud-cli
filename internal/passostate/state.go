package passostate

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"
)

// Ledger stores durable state for a project workflow.
type Ledger struct {
	SchemaVersion int                  `json:"schemaVersion"`
	Profile       string               `json:"profile"`
	ProjectID     int64                `json:"projectId"`
	VersionID     string               `json:"versionId"`
	ReleaseID     string               `json:"releaseId"`
	RunID         string               `json:"runId"`
	Cursor        string               `json:"cursor"`
	Operations    map[string]Operation `json:"operations"`
}

// Operation records the current state of one idempotent operation.
type Operation struct {
	Key       string    `json:"key"`
	State     string    `json:"state"`
	UpdatedAt time.Time `json:"updatedAt"`
}

// Evidence is metadata describing one workflow stage. It must not contain payloads or credentials.
type Evidence struct {
	SchemaVersion int       `json:"schemaVersion"`
	Profile       string    `json:"profile"`
	ProjectID     int64     `json:"projectId"`
	Stage         string    `json:"stage"`
	Status        string    `json:"status"`
	RequestID     string    `json:"requestId"`
	VersionID     string    `json:"versionId"`
	ReleaseID     string    `json:"releaseId"`
	RunID         string    `json:"runId"`
	StartedAt     time.Time `json:"startedAt"`
	FinishedAt    time.Time `json:"finishedAt"`
}

// Load reads a ledger, returning a new version-one ledger when path does not exist.
func Load(path string) (Ledger, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return Ledger{SchemaVersion: 1, Operations: make(map[string]Operation)}, nil
	}
	if err != nil {
		return Ledger{}, fmt.Errorf("read ledger: %w", err)
	}
	var ledger Ledger
	decoder := json.NewDecoder(bytes.NewReader(data))
	if err := decoder.Decode(&ledger); err != nil {
		return Ledger{}, errors.New("invalid ledger JSON")
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return Ledger{}, errors.New("invalid trailing ledger JSON")
	}
	if ledger.SchemaVersion != 1 {
		return Ledger{}, errors.New("unsupported ledger schema version")
	}
	if ledger.ProjectID < 0 {
		return Ledger{}, errors.New("invalid ledger project ID")
	}
	if ledger.Operations == nil {
		ledger.Operations = make(map[string]Operation)
	}
	return ledger, nil
}

// Save atomically replaces path with a private ledger file.
func Save(path string, ledger Ledger) error {
	if ledger.SchemaVersion != 1 {
		return errors.New("unsupported ledger schema version")
	}
	if ledger.ProjectID < 0 {
		return errors.New("invalid ledger project ID")
	}
	if ledger.Operations == nil {
		ledger.Operations = make(map[string]Operation)
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return fmt.Errorf("create ledger directory: %w", err)
	}
	data, err := json.Marshal(ledger)
	if err != nil {
		return errors.New("encode ledger")
	}
	temp, err := os.CreateTemp(dir, ".ledger-*.tmp")
	if err != nil {
		return fmt.Errorf("create ledger temporary file: %w", err)
	}
	tempName := temp.Name()
	defer os.Remove(tempName)
	if err := temp.Chmod(0600); err != nil {
		temp.Close()
		return fmt.Errorf("secure ledger temporary file: %w", err)
	}
	if _, err := temp.Write(data); err != nil {
		temp.Close()
		return fmt.Errorf("write ledger temporary file: %w", err)
	}
	if err := temp.Sync(); err != nil {
		temp.Close()
		return fmt.Errorf("sync ledger temporary file: %w", err)
	}
	if err := temp.Close(); err != nil {
		return fmt.Errorf("close ledger temporary file: %w", err)
	}
	if err := os.Rename(tempName, path); err != nil {
		return fmt.Errorf("replace ledger: %w", err)
	}
	return nil
}

// IdempotencyKey hashes stable JSON for the profile, project, stage, and input.
func IdempotencyKey(profile string, projectID int64, stage string, input json.RawMessage) (string, error) {
	var canonicalInput any = map[string]any{}
	if len(bytes.TrimSpace(input)) > 0 {
		decoder := json.NewDecoder(bytes.NewReader(input))
		decoder.UseNumber()
		if err := decoder.Decode(&canonicalInput); err != nil {
			return "", errors.New("invalid idempotency input JSON")
		}
		var extra any
		if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
			return "", errors.New("invalid trailing idempotency input JSON")
		}
	}
	encoded, err := json.Marshal([]any{profile, projectID, stage, canonicalInput})
	if err != nil {
		return "", errors.New("encode idempotency input")
	}
	digest := sha256.Sum256(encoded)
	return hex.EncodeToString(digest[:]), nil
}

// WriteEvidence writes a unique, private JSON evidence file and returns its path.
func WriteEvidence(dir string, evidence Evidence) (string, error) {
	if evidence.SchemaVersion != 1 {
		return "", errors.New("unsupported evidence schema version")
	}
	if evidence.ProjectID < 0 {
		return "", errors.New("invalid evidence project ID")
	}
	if err := os.MkdirAll(dir, 0700); err != nil {
		return "", fmt.Errorf("create evidence directory: %w", err)
	}
	file, err := os.CreateTemp(dir, "evidence-*.json")
	if err != nil {
		return "", fmt.Errorf("create evidence file: %w", err)
	}
	path := file.Name()
	ok := false
	defer func() {
		if !ok {
			os.Remove(path)
		}
	}()
	if err := file.Chmod(0600); err != nil {
		file.Close()
		return "", fmt.Errorf("secure evidence file: %w", err)
	}
	if err := json.NewEncoder(file).Encode(evidence); err != nil {
		file.Close()
		return "", errors.New("encode evidence")
	}
	if err := file.Sync(); err != nil {
		file.Close()
		return "", fmt.Errorf("sync evidence file: %w", err)
	}
	if err := file.Close(); err != nil {
		return "", fmt.Errorf("close evidence file: %w", err)
	}
	ok = true
	return path, nil
}
