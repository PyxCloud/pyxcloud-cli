package passocli

import (
	"encoding/json"
	"fmt"
	"github.com/pyxcloud/pyxcloud-cli/internal/passocontract"
	"github.com/spf13/cobra"
)

// Input help and catalogue metadata derive from the same versioned OpenAPI body.
func describeOperationInput(cmd *cobra.Command, operation string) {
	raw := passocontract.OperationRequestBodies[operation]
	if raw == "" {
		if op, ok := passocontract.Operations[operation]; ok && op.Method != "GET" {
			cmd.Long = "No request body. Server derives the operation scope from the authenticated project."
		}
		return
	}
	var body struct {
		Required bool `json:"required"`
		Content  map[string]struct {
			Schema json.RawMessage `json:"schema"`
		} `json:"content"`
	}
	if json.Unmarshal([]byte(raw), &body) != nil {
		return
	}
	schema := body.Content["application/json"].Schema
	if len(schema) == 0 {
		return
	}
	var details struct {
		Example json.RawMessage `json:"example"`
	}
	json.Unmarshal(schema, &details)
	cmd.Annotations = map[string]string{"inputSchema": string(schema), "bodyRequired": fmt.Sprint(body.Required)}
	cmd.Long = "Request JSON schema (from the versioned API contract):\n" + string(schema)
	if len(details.Example) > 0 {
		cmd.Annotations["inputExample"] = string(details.Example)
		cmd.Example = "  printf '%s' '" + string(details.Example) + "' | passo " + "projects " + cmd.Name() + " --input -"
	}
}
