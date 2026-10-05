// Package pyxfile implements a local (no-network) parser for the Pyxfile
// declarative IaC format and compiles it into the canonical plan JSON.
//
// Format reference: ops/docs/pyxfile/SPEC.md v1 (Dockerfile-style uppercase
// directives + embedded ARCH block) and the canonical component vocabulary of
// platform/terraform-provider-pyxcloud/SPEC.md §3.1.
//
// Supported subset:
//
//	PYX 1
//	APP <name>
//	ENV <name>
//	WHERE <deploy-area>
//	PROVIDER <name> [REGION <name>]
//	REPO <url>
//	USE <type> AS <name> [WITH feat AND feat ...]
//	ARCH:
//	  place prod:
//	    network public:
//	      expose: [80, 443]
//	      machines:
//	        web:
//	          size: 2cpu/8gb
//	          os: debian/12
//	      services:
//	        db:
//	          type: managed-database
//	          size: small
package pyxfile

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// canonicalTypes is the canonical component vocabulary. Source of truth:
// platform/terraform-provider-pyxcloud/SPEC.md §3.1 (component task table).
var canonicalTypes = map[string]bool{
	"network": true, "vpc": true, "security-group": true,
	"virtual-machine": true, "virtual-machine-scale-group": true,
	"load-balancer": true, "managed-database": true,
	"object-storage": true, "blob-storage": true,
	"cache": true, "managed-queue": true, "event-streaming": true,
	"dns-zone": true, "cdn-service": true, "waf-service": true,
	"managed-kubernetes": true, "container-service": true,
	"serverless-function": true, "secrets-manager": true, "access-policy": true,
}

// typeAliases maps accepted shorthand names to canonical types. The shorthand
// set mirrors the vocabulary listed in the FASE B task (dns, cdn, waf, queue,
// kubernetes, serverless, secrets, email, monitoring, vm-volume). email-service,
// monitoring-service and vm-volume extend the §3.1 table per that task's vocabulary.
var typeAliases = map[string]string{
	"vm":            "virtual-machine",
	"dns":           "dns-zone",
	"cdn":           "cdn-service",
	"waf":           "waf-service",
	"queue":         "managed-queue",
	"kubernetes":    "managed-kubernetes",
	"serverless":    "serverless-function",
	"secrets":       "secrets-manager",
	"email":         "email-service",
	"monitoring":    "monitoring-service",
	"vm-volume":     "vm-volume",
	"event-bus":     "event-streaming",
	"message-queue": "managed-queue",
}

// ResolveType canonicalizes a component type name. It returns "" when the
// type is unknown to the vocabulary.
func ResolveType(t string) string {
	t = strings.ToLower(strings.TrimSpace(t))
	if canonicalTypes[t] {
		return t
	}
	return typeAliases[t]
}

// KnownTypes returns the sorted list of accepted component type names
// (canonical types plus aliases) — used in error messages and tests.
func KnownTypes() []string {
	set := map[string]bool{}
	for t := range canonicalTypes {
		set[t] = true
	}
	for a := range typeAliases {
		set[a] = true
	}
	out := make([]string, 0, len(set))
	for t := range set {
		out = append(out, t)
	}
	sort.Strings(out)
	return out
}

// Component is one canonical component of the compiled plan.
type Component struct {
	Name     string   `json:"name"`
	Type     string   `json:"type"` // canonical type (alias-resolved)
	Size     string   `json:"size,omitempty"`
	Region   string   `json:"region,omitempty"`
	OS       string   `json:"os,omitempty"`
	Place    string   `json:"place,omitempty"`
	Network  string   `json:"network,omitempty"`
	Expose   []int    `json:"expose,omitempty"`
	Features []string `json:"features,omitempty"`
}

// Plan is the canonical plan emitted by "pyx plan".
type Plan struct {
	SpecVersion int         `json:"specVersion"`
	App         string      `json:"app"`
	Env         string      `json:"env"`
	DeployArea  string      `json:"deployArea,omitempty"`
	Provider    string      `json:"provider,omitempty"`
	Region      string      `json:"region,omitempty"` // abstract region from PROVIDER ... REGION
	Repo        string      `json:"repo,omitempty"`
	Components  []Component `json:"components"`
	Warnings    []string    `json:"warnings,omitempty"`
}

// ParseError is a validation/parse error with a 1-based line reference.
type ParseError struct {
	Line int
	Msg  string
}

func (e *ParseError) Error() string {
	if e.Line > 0 {
		return fmt.Sprintf("pyxfile:%d: %s", e.Line, e.Msg)
	}
	return "pyxfile: " + e.Msg
}

func perr(line int, format string, a ...interface{}) error {
	return &ParseError{Line: line, Msg: fmt.Sprintf(format, a...)}
}

type parser struct {
	plan    Plan
	warn    []string
	comps   []*Component // working list; converted to plan.Components at the end
	inArch  bool
	place   string // last "place <name>:" seen
	net     *Component
	section string     // "machines" or "services" while inside one
	cur     *Component // component being filled from an ARCH named entry
}

// Parse parses the Pyxfile text and compiles the canonical plan.
// It never performs network calls.
func Parse(text string) (*Plan, error) {
	p := &parser{}
	first := true
	for i, raw := range strings.Split(text, "\n") {
		line := i + 1
		s := strings.TrimSpace(stripComment(raw))
		if s == "" {
			continue
		}
		if p.inArch {
			if isTopDirective(raw) {
				// An unindented uppercase directive ends the ARCH block.
				p.inArch = false
				if err := p.flush(); err != nil {
					return nil, err
				}
				p.place, p.net, p.section = "", nil, ""
			} else {
				if err := p.archLine(line, s); err != nil {
					return nil, err
				}
				continue
			}
		}
		if err := p.directive(line, s, first); err != nil {
			return nil, err
		}
		first = false
	}
	if first {
		return nil, perr(0, "empty Pyxfile")
	}
	if err := p.finish(); err != nil {
		return nil, err
	}
	p.plan.Warnings = p.warn
	return &p.plan, nil
}

// finish validates the compiled plan and flushes any pending ARCH entry.
func (p *parser) finish() error {
	if err := p.flush(); err != nil {
		return err
	}
	if p.plan.SpecVersion != 1 {
		return perr(0, "missing PYX 1 header (must be the first directive)")
	}
	if p.plan.App == "" {
		return perr(0, "missing APP directive")
	}
	if p.plan.Env == "" {
		return perr(0, "missing ENV directive")
	}
	if len(p.comps) == 0 {
		return perr(0, "no components declared (use USE <type> AS <name> or an ARCH block)")
	}
	for _, c := range p.comps {
		p.plan.Components = append(p.plan.Components, *c)
	}
	return nil
}

// flush validates and appends the component currently being filled from ARCH.
func (p *parser) flush() error {
	c := p.cur
	if c == nil {
		return nil
	}
	p.cur = nil
	if c.Type == "" {
		return perr(0, "component %q is missing its type (add a 'type:' attribute)", c.Name)
	}
	p.comps = append(p.comps, c)
	return nil
}

// topDirectives are the uppercase verbs that, appearing unindented, end the
// embedded ARCH block (per ops/docs/pyxfile/SPEC.md the block is YAML-indented
// and directive-style lines resume at column 0).
var topDirectives = map[string]bool{
	"PYX": true, "APP": true, "ENV": true, "WHERE": true,
	"PROVIDER": true, "REPO": true, "USE": true, "INSTALL": true,
	"RUN": true, "EXPOSE": true,
}

func isTopDirective(raw string) bool {
	if raw == "" || raw[0] == ' ' || raw[0] == '\t' {
		return false
	}
	verb, _ := splitVerb(strings.TrimSpace(stripComment(raw)))
	return topDirectives[verb]
}

func stripComment(s string) string {
	// '#' starts a comment; Pyxfile values in this subset are simple tokens
	// or bracket lists, so cutting at the first '#' is safe.
	if idx := strings.IndexByte(s, '#'); idx >= 0 {
		return s[:idx]
	}
	return s
}

func splitVerb(s string) (verb, rest string) {
	verb, rest, _ = strings.Cut(s, " ")
	return verb, strings.TrimSpace(rest)
}

func (p *parser) directive(line int, s string, first bool) error {
	verb, rest := splitVerb(s)
	switch verb {
	case "PYX":
		v, err := strconv.Atoi(strings.TrimSpace(rest))
		if err != nil || v != 1 {
			return perr(line, "unsupported spec version %q (only PYX 1 is supported)", strings.TrimSpace(rest))
		}
		p.plan.SpecVersion = v
		return nil
	case "APP":
		if rest == "" {
			return perr(line, "APP requires a name")
		}
		p.plan.App = rest
		return nil
	case "ENV":
		if rest == "" {
			return perr(line, "ENV requires a tag")
		}
		p.plan.Env = rest
		return nil
	case "WHERE":
		if rest == "" {
			return perr(line, "WHERE requires a deploy area")
		}
		p.plan.DeployArea = rest
		return nil
	case "PROVIDER":
		f := strings.Fields(rest)
		if len(f) == 0 {
			return perr(line, "PROVIDER requires a name")
		}
		p.plan.Provider = f[0]
		for i := 1; i < len(f); i++ {
			if strings.EqualFold(f[i], "REGION") {
				region := strings.TrimSpace(strings.Join(f[i+1:], " "))
				if region == "" {
					return perr(line, "PROVIDER REGION requires a value")
				}
				p.plan.Region = strings.Trim(region, `"`)
				return nil
			}
			p.warn = append(p.warn, fmt.Sprintf("line %d: ignoring PROVIDER argument %q", line, f[i]))
		}
		return nil
	case "REPO":
		if rest == "" {
			return perr(line, "REPO requires a URL")
		}
		p.plan.Repo = rest
		return nil
	case "USE":
		return p.use(line, rest)
	case "ARCH:":
		p.inArch = true
		return nil
	default:
		if first {
			return perr(line, "first directive must be PYX 1, got %q", verb)
		}
		return perr(line, "unknown directive %q (expected APP, ENV, WHERE, PROVIDER, REPO, USE or ARCH:)", verb)
	}
}

// USE <type> AS <name> [WITH f AND f ...]
func (p *parser) use(line int, rest string) error {
	f := strings.Fields(rest)
	if len(f) < 3 || !strings.EqualFold(f[1], "AS") {
		return perr(line, `USE requires "USE <type> AS <name>"`)
	}
	typ := ResolveType(f[0])
	if typ == "" {
		return perr(line, "unknown component type %q (must be one of: %s)", f[0], strings.Join(KnownTypes(), ", "))
	}
	c := Component{Name: f[2], Type: typ}
	if len(f) > 3 {
		if !strings.EqualFold(f[3], "WITH") {
			return perr(line, `expected WITH after "USE %s AS %s", got %q`, f[0], f[2], f[3])
		}
		for _, feat := range f[4:] {
			if strings.EqualFold(feat, "AND") {
				continue
			}
			c.Features = append(c.Features, feat)
		}
	}
	p.comps = append(p.comps, &c)
	return nil
}

// archLine handles one line inside the ARCH: block (minimal YAML subset,
// indentation-insensitive: structure is driven by the "place"/"network" and
// "machines"/"services" markers).
func (p *parser) archLine(line int, s string) error {
	if s == "ARCH:" {
		return nil
	}
	if strings.HasSuffix(s, ":") {
		head := s[:len(s)-1]
		f := strings.Fields(head)
		switch {
		case len(f) == 2 && f[0] == "place":
			if err := p.flush(); err != nil {
				return err
			}
			p.place, p.net, p.section = f[1], nil, ""
			return nil
		case len(f) == 2 && f[0] == "network":
			if err := p.flush(); err != nil {
				return err
			}
			p.net = &Component{Type: "network", Name: f[1], Place: p.place}
			p.comps = append(p.comps, p.net)
			p.section = ""
			return nil
		case head == "machines" || head == "services":
			if p.net == nil {
				return perr(line, "%q must be nested inside a network block", head)
			}
			p.section = head
			return nil
		case head == "expose":
			return nil // handled as "expose: [..]" below
		default:
			if p.section == "" {
				return perr(line, "unexpected entry %q inside ARCH (expected place/network/machines/services)", head)
			}
			if err := p.flush(); err != nil {
				return err
			}
			p.cur = &Component{Name: head, Place: p.place, Network: p.net.Name}
			if p.section == "machines" {
				p.cur.Type = "virtual-machine" // machines: entries are VMs by default
			}
			return nil
		}
	}
	// key: value attribute lines.
	k, v, ok := strings.Cut(s, ":")
	if !ok {
		return perr(line, "cannot parse ARCH line %q", s)
	}
	k, v = strings.TrimSpace(k), strings.TrimSpace(v)
	switch k {
	case "expose":
		if p.net == nil {
			return perr(line, "expose must be inside a network block")
		}
		ports, err := parsePorts(v)
		if err != nil {
			return perr(line, "bad expose list %q: %v", v, err)
		}
		p.net.Expose = append(p.net.Expose, ports...)
		return nil
	case "size", "os", "region":
		if p.cur == nil {
			return perr(line, "%q attribute outside a component entry", k)
		}
		switch k {
		case "size":
			p.cur.Size = v
		case "os":
			p.cur.OS = v
		case "region":
			p.cur.Region = v
		}
		return nil
	case "type":
		if p.cur == nil {
			return perr(line, "%q attribute outside a component entry", k)
		}
		typ := ResolveType(v)
		if typ == "" {
			return perr(line, "unknown component type %q (must be one of: %s)", v, strings.Join(KnownTypes(), ", "))
		}
		p.cur.Type = typ
		return nil
	default:
		return perr(line, "unknown ARCH attribute %q (expected size, os, region, type, expose)", k)
	}
}

// parsePorts parses "[80, 443]" (brackets optional) into a port list.
func parsePorts(v string) ([]int, error) {
	v = strings.Trim(strings.TrimSpace(v), "[]")
	if v == "" {
		return nil, nil
	}
	var ports []int
	for _, f := range strings.FieldsFunc(v, func(r rune) bool { return r == ',' }) {
		p, err := strconv.Atoi(strings.TrimSpace(f))
		if err != nil || p <= 0 || p > 65535 {
			return nil, fmt.Errorf("invalid port %q", f)
		}
		ports = append(ports, p)
	}
	return ports, nil
}
