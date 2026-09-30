package passocli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"time"

	"github.com/pyxcloud/pyxcloud-cli/internal/passocontract"
	"github.com/pyxcloud/pyxcloud-cli/internal/passostate"
	"github.com/pyxcloud/pyxcloud-cli/internal/passotransport"
)

const maxInputBytes = 1 << 20

// ReadInput reads an optional JSON object from a file or stdin (-).
func ReadInput(path string, in io.Reader) (json.RawMessage, error) {
	if path == "" {
		return nil, nil
	}
	var reader io.Reader
	if path == "-" {
		reader = in
	} else {
		f, err := os.Open(path)
		if err != nil {
			return nil, &ExitError{20, "invalid_input"}
		}
		defer f.Close()
		reader = f
	}
	if reader == nil {
		return nil, &ExitError{20, "invalid_input"}
	}
	b, err := io.ReadAll(io.LimitReader(reader, maxInputBytes+1))
	if err != nil || len(b) > maxInputBytes {
		return nil, &ExitError{20, "invalid_input"}
	}
	var obj map[string]json.RawMessage
	d := json.NewDecoder(bytes.NewReader(b))
	if err = d.Decode(&obj); err != nil || obj == nil {
		return nil, &ExitError{20, "invalid_input"}
	}
	var extra any
	if err = d.Decode(&extra); !errors.Is(err, io.EOF) {
		return nil, &ExitError{20, "invalid_input"}
	}
	return json.RawMessage(b), nil
}

// Perform invokes one generated API operation and records mutation metadata.
func (r *Runtime) Perform(ctx context.Context, stage, operationKey string, params map[string]string, query url.Values, input json.RawMessage, bodyIdempotency bool) (Result, error) {
	var empty Result
	op, ok := passocontract.Operations[operationKey]
	if !ok {
		return empty, &ExitError{20, "unknown_operation"}
	}
	if !r.matchesScope(op, params) {
		return empty, &ExitError{20, "scope_mismatch"}
	}
	var bodyObject map[string]json.RawMessage
	if len(bytes.TrimSpace(input)) > 0 {
		dec := json.NewDecoder(bytes.NewReader(input))
		if err := dec.Decode(&bodyObject); err != nil || bodyObject == nil {
			return empty, &ExitError{20, "invalid_input"}
		}
		var extra any
		if err := dec.Decode(&extra); !errors.Is(err, io.EOF) {
			return empty, &ExitError{20, "invalid_input"}
		}
	}
	if _, supplied := bodyObject["idempotencyKey"]; supplied {
		return empty, &ExitError{20, "managed_idempotency_key"}
	}
	canonical, err := json.Marshal(map[string]any{"params": params, "query": query, "body": bodyObject})
	if err != nil {
		return empty, &ExitError{20, "invalid_input"}
	}
	key, err := passostate.IdempotencyKey(r.Profile.Name, r.ProjectID, operationKey, canonical)
	if err != nil {
		return empty, &ExitError{20, "invalid_input"}
	}
	if bodyIdempotency {
		if bodyObject == nil {
			bodyObject = map[string]json.RawMessage{}
		}
		encodedKey, _ := json.Marshal(key)
		bodyObject["idempotencyKey"] = encodedKey
	}
	var body json.RawMessage
	if bodyObject != nil {
		body, err = json.Marshal(bodyObject)
		if err != nil {
			return empty, &ExitError{20, "invalid_input"}
		}
	}
	mutation := op.Method != http.MethodGet
	now := time.Now
	if r.now != nil {
		now = r.now
	}
	started := now()
	if mutation {
		if r.Ledger.SchemaVersion == 0 {
			r.Ledger.SchemaVersion = 1
		}
		if r.Ledger.Operations == nil {
			r.Ledger.Operations = map[string]passostate.Operation{}
		}
		r.Ledger.Profile, r.Ledger.ProjectID = r.Profile.Name, r.ProjectID
		r.Ledger.VersionID, r.Ledger.VersionSequence, r.Ledger.ReleaseID, r.Ledger.RunID = r.VersionID, r.VersionSequence, r.ReleaseID, r.RunID
		r.Ledger.Operations[operationKey] = passostate.Operation{Key: key, State: "pending", UpdatedAt: started}
		if err := passostate.Save(r.LedgerPath, r.Ledger); err != nil {
			return empty, &ExitError{20, "ledger_write_failed"}
		}
	}
	timeout := r.timeout
	if timeout <= 0 {
		timeout = 2 * time.Minute
	}
	callCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	resp, callErr := passocontract.Invoke(callCtx, r.Client, operationKey, params, query, body, key)
	if callErr != nil {
		if mutation {
			state := "uncertain"
			var api *passotransport.APIError
			if errors.As(callErr, &api) && api.StatusCode >= 400 && api.StatusCode < 500 && api.StatusCode != http.StatusRequestTimeout {
				state = "failed"
			}
			r.Ledger.Operations[operationKey] = passostate.Operation{Key: key, State: state, UpdatedAt: now()}
			_ = passostate.Save(r.LedgerPath, r.Ledger)
			status := "failed"
			if state == "uncertain" {
				status = "uncertain"
			}
			_, _ = passostate.WriteEvidence(r.EvidenceDir, passostate.Evidence{SchemaVersion: 1, Profile: r.Profile.Name, ProjectID: r.ProjectID, Stage: stage, Status: status, StartedAt: started, FinishedAt: now()})
		}
		return empty, classify(callErr)
	}
	if len(bytes.TrimSpace(resp.Body)) > 0 && !json.Valid(resp.Body) {
		if mutation {
			r.Ledger.Operations[operationKey] = passostate.Operation{Key: key, State: "uncertain", UpdatedAt: now()}
			_ = passostate.Save(r.LedgerPath, r.Ledger)
		}
		return empty, &ExitError{30, "invalid_api_response"}
	}
	oldProject, oldVersion, oldSequence, oldRelease, oldRun := r.ProjectID, r.VersionID, r.VersionSequence, r.ReleaseID, r.RunID
	if err := r.observeIDs(resp.Body, operationKey); err != nil {
		if mutation {
			r.Ledger.Operations[operationKey] = passostate.Operation{Key: key, State: "uncertain", UpdatedAt: now()}
			_ = passostate.Save(r.LedgerPath, r.Ledger)
		}
		return empty, err
	}
	status := "observed"
	ledgerState := ""
	idsChanged := oldProject != r.ProjectID || oldVersion != r.VersionID || oldSequence != r.VersionSequence || oldRelease != r.ReleaseID || oldRun != r.RunID
	if mutation {
		status, ledgerState = "accepted", "accepted"
		if resp.StatusCode == http.StatusOK || resp.StatusCode == http.StatusCreated {
			ledgerState, status = "completed", "completed"
		}
		r.Ledger.Operations[operationKey] = passostate.Operation{Key: key, State: ledgerState, UpdatedAt: now()}
	}
	if mutation || idsChanged {
		if r.Ledger.SchemaVersion == 0 {
			r.Ledger.SchemaVersion = 1
		}
		r.Ledger.Profile = r.Profile.Name
		r.Ledger.VersionID, r.Ledger.VersionSequence, r.Ledger.ReleaseID, r.Ledger.RunID = r.VersionID, r.VersionSequence, r.ReleaseID, r.RunID
		if err := passostate.Save(r.LedgerPath, r.Ledger); err != nil {
			return empty, &ExitError{20, "ledger_write_failed"}
		}
	}
	path, evidenceErr := passostate.WriteEvidence(r.EvidenceDir, passostate.Evidence{SchemaVersion: 1, Profile: r.Profile.Name, ProjectID: r.ProjectID, Stage: stage, Status: status, RequestID: resp.RequestID, VersionID: r.VersionID, ReleaseID: r.ReleaseID, RunID: r.RunID, StartedAt: started, FinishedAt: now()})
	if evidenceErr != nil {
		return empty, &ExitError{20, "evidence_write_failed"}
	}
	resultStatus := "observed"
	if mutation {
		resultStatus = "accepted"
	}
	return Result{Stage: stage, Status: resultStatus, ProjectID: r.ProjectID, VersionID: r.VersionID, VersionSequence: r.VersionSequence, ReleaseID: r.ReleaseID, RunID: r.RunID, Data: resp.Body, Evidence: evidencePaths(path)}, nil
}

func (r *Runtime) matchesScope(op passocontract.Operation, params map[string]string) bool {
	projectParam := "projectId"
	if op.Contract == "vibe-docs-boardos" {
		projectParam = "id"
	}
	if value, ok := params[projectParam]; ok {
		id, err := strconv.ParseInt(value, 10, 64)
		if err != nil || id != r.ProjectID {
			return false
		}
	}
	versionScope := r.VersionID
	if usesVersionSequence(op) {
		if r.VersionSequence <= 0 {
			return false
		}
		versionScope = strconv.FormatInt(r.VersionSequence, 10)
	}
	for _, scope := range []struct{ key, value string }{{"versionId", versionScope}, {"releaseId", r.ReleaseID}, {"runId", r.RunID}} {
		if expected, ok := params[scope.key]; ok && scope.value != "" && expected != scope.value {
			return false
		}
	}
	return true
}

func usesVersionSequence(op passocontract.Operation) bool {
	// These API contracts accept the numeric frozen-version sequence in their
	// versionId path slot; release operations continue to use the UUID.
	switch op.Contract {
	case "regioncompare", "regioncompare.v2", "securitygate", "securityscan":
		return true
	default:
		return false
	}
}

func evidencePaths(path string) []string {
	if path == "" {
		return nil
	}
	return []string{path}
}

func (r *Runtime) observeIDs(raw json.RawMessage, operationKey string) error {
	if len(bytes.TrimSpace(raw)) == 0 {
		return nil
	}
	var top map[string]json.RawMessage
	if json.Unmarshal(raw, &top) != nil {
		return nil
	}
	obj := top
	if data, ok := top["data"]; ok {
		var nested map[string]json.RawMessage
		if json.Unmarshal(data, &nested) == nil && nested != nil {
			obj = nested
		}
	}
	readString := func(k string) string { var v string; _ = json.Unmarshal(obj[k], &v); return v }
	readInt := func(k string) int64 { var v int64; _ = json.Unmarshal(obj[k], &v); return v }
	if id := readInt("projectId"); id > 0 {
		if r.ProjectID > 0 && r.ProjectID != id {
			return &ExitError{30, "project_id_mismatch"}
		}
		r.ProjectID = id
		r.Ledger.ProjectID = id
	} else if id := readInt("id"); id > 0 && operationKey == "projects:projectCreate" {
		r.ProjectID, r.Ledger.ProjectID = id, id
	}
	if v := readString("versionId"); v != "" {
		r.VersionID, r.Ledger.VersionID = v, v
	}
	if operationKey == "journeycontract:releaseFreezeCreate" {
		if sequence := readInt("versionSequence"); sequence > 0 {
			r.VersionSequence, r.Ledger.VersionSequence = sequence, sequence
		}
	}
	if v := readString("releaseId"); v != "" {
		r.ReleaseID, r.Ledger.ReleaseID = v, v
	}
	if v := readString("runId"); v != "" {
		r.RunID, r.Ledger.RunID = v, v
	}
	return nil
}
