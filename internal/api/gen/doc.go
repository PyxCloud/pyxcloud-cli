// Package gen is the root of the Go clients generated with oapi-codegen from
// the pyx-backend OpenAPI contracts vendored in api/contracts (pinned commit in
// api/contracts/SOURCE).
//
// Layout: one package per contract, internal/api/gen/<name>, where <name> is
// the contract file name without ".openapi.json" and with "-" and "."
// removed (specops-transitions.openapi.json -> specopstransitions). Each
// package holds a generate.go with its go:generate directive, the
// oapi-codegen.yaml config and the generated <name>.gen.go. Contracts are kept
// in separate packages because they are independent specs that reuse
// component names (Blocker, Capability, Freshness, ApiError, ...) with
// different shapes; merging them would need renaming or conflict resolution
// that the contracts do not define.
//
// Regenerate everything with:
//
//	go generate ./internal/api/gen/...
//
// Contracts listed in NotGenerated are vendored but have no package, with the
// reason; TestEveryContractIsGeneratedOrListed keeps the two sets in sync.
package gen

// NotGenerated lists vendored contracts that oapi-codegen cannot turn into a
// client, keyed by file name, with the reason. Fix upstream in pyx-backend,
// re-sync, then add the package and drop the entry.
var NotGenerated = map[string]string{
	"deployment.openapi.json":  "invalid OpenAPI: components.schemas.*.properties hold example values (0, \"\", []) instead of schema objects",
	"environment.openapi.json": "invalid OpenAPI: components.schemas.*.properties hold example values (0, \"\", []) instead of schema objects",
}
