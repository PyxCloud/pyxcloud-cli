//go:build tools

// Package tools pins build-time tool versions in go.mod so `go generate`
// and CI use exactly the same code generator. It is excluded from normal
// builds by the "tools" build tag.
package tools

import (
	_ "github.com/oapi-codegen/oapi-codegen/v2/cmd/oapi-codegen"
)
