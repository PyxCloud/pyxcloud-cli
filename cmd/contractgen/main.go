package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"go/format"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

type doc struct {
	Paths      map[string]map[string]operation `json:"paths"`
	Components struct {
		Schemas    map[string]schema    `json:"schemas"`
		Parameters map[string]parameter `json:"parameters"`
	} `json:"components"`
}
type operation struct {
	OperationID string          `json:"operationId"`
	RequestBody json.RawMessage `json:"requestBody"`
	Parameters  []parameter     `json:"parameters"`
}
type parameter struct {
	Ref      string `json:"$ref"`
	Name     string `json:"name"`
	In       string `json:"in"`
	Required bool   `json:"required"`
}
type schema struct {
	Type       string                     `json:"type"`
	Ref        string                     `json:"$ref"`
	Format     string                     `json:"format"`
	Properties map[string]json.RawMessage `json:"properties"`
	Required   []string                   `json:"required"`
	Items      json.RawMessage            `json:"items"`
	Additional json.RawMessage            `json:"additionalProperties"`
	Enum       []string                   `json:"enum"`
	OneOf      []json.RawMessage          `json:"oneOf"`
	AnyOf      []json.RawMessage          `json:"anyOf"`
	AllOf      []json.RawMessage          `json:"allOf"`
}
type opEntry struct {
	Key, Contract, ID, Method, Path string
	Body                            json.RawMessage
	Params                          []string
}

func main() {
	check := flag.Bool("check", false, "check generated files without writing")
	flag.Parse()
	if err := run(*check); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
func run(check bool) error {
	files, err := filepath.Glob("contracts/*.openapi.json")
	if err != nil {
		return err
	}
	sort.Strings(files)
	entries := []opEntry{}
	var journey map[string]schema
	for _, file := range files {
		b, err := os.ReadFile(file)
		if err != nil {
			return err
		}
		var d doc
		if err = json.Unmarshal(b, &d); err != nil {
			return fmt.Errorf("%s: %w", file, err)
		}
		var pathItems struct {
			Paths map[string]struct {
				Parameters []json.RawMessage `json:"parameters"`
			} `json:"paths"`
		}
		if err = json.Unmarshal(b, &pathItems); err != nil {
			return fmt.Errorf("%s: %w", file, err)
		}
		for path, item := range pathItems.Paths {
			if len(item.Parameters) > 0 {
				return fmt.Errorf("%s: path-level parameters at %s are unsupported; declare them on each operation", file, path)
			}
		}
		stem := strings.TrimSuffix(filepath.Base(file), ".openapi.json")
		if stem == "journey" {
			journey = d.Components.Schemas
		}
		paths := make([]string, 0, len(d.Paths))
		for p := range d.Paths {
			paths = append(paths, p)
		}
		sort.Strings(paths)
		for _, p := range paths {
			methods := make([]string, 0, len(d.Paths[p]))
			for m := range d.Paths[p] {
				methods = append(methods, m)
			}
			sort.Strings(methods)
			for _, m := range methods {
				o := d.Paths[p][m]
				if o.OperationID == "" {
					continue
				}
				resolvedParameters, resolveErr := resolveParameters(o.Parameters, d.Components.Parameters)
				if resolveErr != nil {
					return fmt.Errorf("%s %s %s parameters: %w", file, strings.ToUpper(m), p, resolveErr)
				}
				key := stem + ":" + o.OperationID
				params := []string{}
				for _, v := range resolvedParameters {
					if v.In == "path" {
						if !v.Required {
							return fmt.Errorf("%s %s %s path parameter %s is not required", file, strings.ToUpper(m), p, v.Name)
						}
						params = append(params, v.Name)
					}
				}
				sort.Strings(params)
				entries = append(entries, opEntry{Key: key, Contract: stem, ID: o.OperationID, Method: strings.ToUpper(m), Path: p, Params: params, Body: o.RequestBody})
			}
		}
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Key < entries[j].Key })
	ops, err := generateOps(entries)
	if err != nil {
		return err
	}
	if err = output("internal/passocontract/operations_generated.go", ops, check); err != nil {
		return err
	}
	journeyBytes, err := generateJourney(journey)
	if err != nil {
		return err
	}
	return output("internal/passocontract/journey_generated.go", journeyBytes, check)
}
func resolveParameters(parameters []parameter, definitions map[string]parameter) ([]parameter, error) {
	resolved := make([]parameter, len(parameters))
	for i, item := range parameters {
		value, err := resolveParameter(item, definitions, map[string]bool{})
		if err != nil {
			return nil, fmt.Errorf("parameter %d: %w", i, err)
		}
		if value.Name == "" || (value.In != "path" && value.In != "query" && value.In != "header" && value.In != "cookie") {
			return nil, fmt.Errorf("parameter %d has incomplete name or location", i)
		}
		resolved[i] = value
	}
	return resolved, nil
}

func resolveParameter(value parameter, definitions map[string]parameter, visiting map[string]bool) (parameter, error) {
	if value.Ref == "" {
		return value, nil
	}
	const prefix = "#/components/parameters/"
	if !strings.HasPrefix(value.Ref, prefix) {
		return parameter{}, fmt.Errorf("unsupported parameter reference %q", value.Ref)
	}
	encodedName := strings.TrimPrefix(value.Ref, prefix)
	if encodedName == "" || strings.Contains(encodedName, "/") {
		return parameter{}, fmt.Errorf("unsupported parameter reference %q", value.Ref)
	}
	name, err := decodeJSONPointerToken(encodedName)
	if err != nil {
		return parameter{}, fmt.Errorf("unsupported parameter reference %q", value.Ref)
	}
	if visiting[name] {
		return parameter{}, fmt.Errorf("cyclic parameter reference %q", value.Ref)
	}
	target, ok := definitions[name]
	if !ok {
		return parameter{}, fmt.Errorf("unresolved parameter reference %q", value.Ref)
	}
	visiting[name] = true
	resolved, err := resolveParameter(target, definitions, visiting)
	delete(visiting, name)
	return resolved, err
}

func decodeJSONPointerToken(token string) (string, error) {
	var out strings.Builder
	for i := 0; i < len(token); i++ {
		if token[i] != '~' {
			out.WriteByte(token[i])
			continue
		}
		if i+1 >= len(token) {
			return "", fmt.Errorf("invalid JSON pointer escape")
		}
		i++
		switch token[i] {
		case '0':
			out.WriteByte('~')
		case '1':
			out.WriteByte('/')
		default:
			return "", fmt.Errorf("invalid JSON pointer escape")
		}
	}
	return out.String(), nil
}

func output(path string, data []byte, check bool) error {
	old, err := os.ReadFile(path)
	if check {
		if err != nil || !bytes.Equal(old, data) {
			return fmt.Errorf("generated file drift: %s", path)
		}
		return nil
	}
	if err = os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".contractgen-*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if _, err = tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err = tmp.Chmod(0644); err != nil {
		_ = tmp.Close()
		return err
	}
	if err = tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err = tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpName, path)
}
func generateOps(entries []opEntry) ([]byte, error) {
	var b strings.Builder
	b.WriteString("// Code generated by cmd/contractgen; DO NOT EDIT.\npackage passocontract\n\ntype Operation struct { Contract, ID, Method, Path string }\n\nvar Operations = map[string]Operation{\n")
	for _, e := range entries {
		fmt.Fprintf(&b, "%q: {Contract:%q, ID:%q, Method:%q, Path:%q},\n", e.Key, e.Contract, e.ID, e.Method, e.Path)
	}
	b.WriteString("}\n\nvar operationPathParams = map[string][]string{\n")
	for _, e := range entries {
		if len(e.Params) > 0 {
			fmt.Fprintf(&b, "%q: {%s},\n", e.Key, quoted(e.Params))
		}
	}
	b.WriteString("}\n\nvar OperationRequestBodies = map[string]string{\n")
	for _, e := range entries {
		if len(e.Body) > 0 {
			var compact bytes.Buffer
			if err := json.Compact(&compact, e.Body); err != nil {
				return nil, err
			}
			fmt.Fprintf(&b, "%q:%q,\n", e.Key, compact.String())
		}
	}
	b.WriteString("}\n")
	return format.Source([]byte(b.String()))
}
func quoted(s []string) string {
	q := make([]string, len(s))
	for i, v := range s {
		q[i] = fmt.Sprintf("%q", v)
	}
	return strings.Join(q, ",")
}
func generateJourney(schemas map[string]schema) ([]byte, error) {
	var b strings.Builder
	b.WriteString("// Code generated by cmd/contractgen; DO NOT EDIT.\npackage passocontract\n\nimport \"encoding/json\"\n\n")
	names := make([]string, 0, len(schemas))
	for n := range schemas {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, name := range names {
		s := schemas[name]
		if s.Type != "object" || s.Properties == nil || len(s.OneOf)+len(s.AnyOf)+len(s.AllOf) > 0 {
			continue
		}
		fmt.Fprintf(&b, "type %s struct {\n", name)
		props := make([]string, 0, len(s.Properties))
		for p := range s.Properties {
			props = append(props, p)
		}
		sort.Strings(props)
		req := map[string]bool{}
		for _, p := range s.Required {
			req[p] = true
		}
		for _, p := range props {
			prop := parseSchema(s.Properties[p])
			typ := goType(prop)
			if !req[p] && (prop.Ref != "" || (prop.Type != "array" && prop.Type != "object" && prop.Type != "")) {
				typ = "*" + typ
			}
			fmt.Fprintf(&b, "%s %s `json:%q`\n", exported(p), typ, p)
		}
		b.WriteString("}\n\n")
	}
	return format.Source([]byte(b.String()))
}
func goType(s schema) string {
	if s.Ref != "" {
		return s.Ref[strings.LastIndex(s.Ref, "/")+1:]
	}
	switch s.Type {
	case "string":
		if len(s.Enum) > 0 {
			return "string"
		}
		return "string"
	case "integer":
		return "int64"
	case "number":
		return "float64"
	case "boolean":
		return "bool"
	case "array":
		if len(s.Items) == 0 {
			return "[]json.RawMessage"
		}
		return "[]" + goType(parseSchema(s.Items))
	case "object":
		if s.Properties == nil {
			if len(s.Additional) > 0 {
				return "map[string]" + goType(parseSchema(s.Additional))
			}
			return "json.RawMessage"
		}
		return "json.RawMessage"
	default:
		return "json.RawMessage"
	}
}
func parseSchema(raw json.RawMessage) schema {
	var value schema
	if len(raw) == 0 || json.Unmarshal(raw, &value) != nil {
		return schema{Type: "raw"}
	}
	return value
}
func exported(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}
