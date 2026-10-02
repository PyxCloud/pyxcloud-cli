package passocli

import (
	"bytes"
	"context"
	"encoding/json"
	"net/url"
	"strconv"
	"time"

	"github.com/spf13/cobra"
)

func newDefineApplyCommand(makeRuntime func(*cobra.Command) (*Runtime, error)) *cobra.Command {
	cmd := &cobra.Command{Use: "apply", Short: "Derive and apply the compiled scope contract", Args: cobra.NoArgs}
	cmd.RunE = func(cmd *cobra.Command, _ []string) error {
		r, err := makeRuntime(cmd)
		if err != nil {
			return err
		}
		if r.ProjectID <= 0 {
			return &ExitError{20, "project_required"}
		}
		timeout := r.timeout
		if timeout <= 0 {
			timeout = 2 * time.Minute
		}
		ctx, cancel := context.WithTimeout(cmd.Context(), timeout)
		defer cancel()
		params := map[string]string{"projectId": strconv.FormatInt(r.ProjectID, 10)}

		derived, err := r.Perform(ctx, "scope", "define:scopeDerivation", params, nil, nil, false)
		if err != nil {
			return err
		}
		contract, err := validatedDerivedContract(derived.Data)
		if err != nil {
			return err
		}

		snapshot, err := r.Perform(ctx, "scope", "projects:canonicalProjectStateRead", params, nil, nil, false)
		if err != nil {
			return err
		}
		state, version, existing, err := canonicalScopeSnapshot(snapshot.Data)
		if err != nil {
			return err
		}
		if existing != nil && semanticJSONEqual(existing, contract) {
			return r.Emit(snapshot)
		}
		if state != "BUILD.DEVELOP" && state != "BUILD.SCOPE_ASSESSMENT" {
			return &ExitError{30, "define_state_conflict"}
		}
		body, _ := json.Marshal(map[string]any{
			"event":   "scope.contract.derived",
			"version": version,
			"payload": map[string]json.RawMessage{"scope_contract": contract},
		})
		result, err := r.Perform(ctx, "scope", "projects:canonicalProjectTransition", params, url.Values{}, body, false)
		if err != nil {
			return err
		}
		return r.Emit(result)
	}
	return cmd
}

func validatedDerivedContract(data json.RawMessage) (json.RawMessage, error) {
	var envelope map[string]json.RawMessage
	if json.Unmarshal(data, &envelope) != nil || envelope == nil {
		return nil, invalidDerivedContract()
	}
	contract := bytes.TrimSpace(envelope["scope_contract"])
	var object map[string]json.RawMessage
	if len(contract) == 0 || json.Unmarshal(contract, &object) != nil || object == nil || len(object) == 0 {
		return nil, invalidDerivedContract()
	}
	var version, candidate, source string
	if json.Unmarshal(object["v"], &version) != nil || version == "" || json.Unmarshal(object["candidateId"], &candidate) != nil || candidate == "" || json.Unmarshal(object["source"], &source) != nil || (source != "documentation" && source != "discovery") {
		return nil, invalidDerivedContract()
	}
	topRevision, okTop := positiveJSONInteger(envelope["compilationRevision"])
	contractRevision, okContract := positiveJSONInteger(object["compilationRevision"])
	if !okTop || !okContract || topRevision != contractRevision {
		return nil, invalidDerivedContract()
	}
	return append(json.RawMessage(nil), contract...), nil
}

func invalidDerivedContract() error { return &ExitError{30, "invalid_scope_derivation"} }

func canonicalScopeSnapshot(data json.RawMessage) (string, int64, json.RawMessage, error) {
	var snapshot struct {
		State   string                     `json:"state"`
		Version json.RawMessage            `json:"version"`
		Data    map[string]json.RawMessage `json:"data"`
	}
	if json.Unmarshal(data, &snapshot) != nil || snapshot.State == "" || snapshot.Data == nil {
		return "", 0, nil, &ExitError{30, "invalid_project_state"}
	}
	version, ok := positiveJSONInteger(snapshot.Version)
	if !ok {
		return "", 0, nil, &ExitError{30, "invalid_project_state"}
	}
	contract := bytes.TrimSpace(snapshot.Data["scope_contract"])
	if len(contract) == 0 || bytes.Equal(contract, []byte("null")) {
		return snapshot.State, version, nil, nil
	}
	var object map[string]json.RawMessage
	if json.Unmarshal(contract, &object) != nil || object == nil {
		return "", 0, nil, &ExitError{30, "invalid_project_state"}
	}
	return snapshot.State, version, append(json.RawMessage(nil), contract...), nil
}

func positiveJSONInteger(raw json.RawMessage) (int64, bool) {
	if len(raw) == 0 {
		return 0, false
	}
	var number json.Number
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	if dec.Decode(&number) != nil {
		return 0, false
	}
	value, err := strconv.ParseInt(number.String(), 10, 64)
	return value, err == nil && value > 0
}

func semanticJSONEqual(a, b json.RawMessage) bool {
	var left, right any
	if json.Unmarshal(a, &left) != nil || json.Unmarshal(b, &right) != nil {
		return false
	}
	l, errLeft := json.Marshal(left)
	r, errRight := json.Marshal(right)
	return errLeft == nil && errRight == nil && bytes.Equal(l, r)
}
