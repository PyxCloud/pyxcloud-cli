package passocontract

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"strings"

	"github.com/pyxcloud/pyxcloud-cli/internal/passotransport"
)

var pathSlot = regexp.MustCompile(`\{([^{}]+)\}`)

// Invoke executes a generated operation using the parameter names declared by
// its OpenAPI path. Unknown operation keys and incomplete parameter sets fail
// locally before any network request is made.
func Invoke(ctx context.Context, client *passotransport.Client, operationKey string, parameters map[string]string, query url.Values, body json.RawMessage, key string) (passotransport.Response, error) {
	op, ok := Operations[operationKey]
	if !ok {
		return passotransport.Response{}, fmt.Errorf("unknown operation %q", operationKey)
	}
	path := op.Path
	wanted := map[string]bool{}
	for _, name := range operationPathParams[operationKey] {
		wanted[name] = true
		value, exists := parameters[name]
		if !exists {
			return passotransport.Response{}, fmt.Errorf("missing path parameter %q", name)
		}
		path = strings.ReplaceAll(path, "{"+name+"}", url.PathEscape(value))
	}
	for name := range parameters {
		if !wanted[name] {
			return passotransport.Response{}, fmt.Errorf("unexpected path parameter %q", name)
		}
	}
	if pathSlot.MatchString(path) {
		return passotransport.Response{}, errors.New("unresolved path parameter")
	}
	if encoded := query.Encode(); encoded != "" {
		path += "?" + encoded
	}
	if client == nil {
		return passotransport.Response{}, errors.New("nil API client")
	}
	return client.Do(ctx, op.Method, path, body, key)
}
