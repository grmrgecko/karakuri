package scriptsdk

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"sort"
	"strings"
	"unicode"

	"github.com/grmrgecko/karakuri/pkg/flow"
)

// Functions derives the callable subset of a node catalog. Canonical names are
// exact registry types, and only aliases that cannot collide with another
// function or the generic call property are advertised.
func Functions(metas []flow.NodeMeta) []Function {
	functions := make([]Function, 0, len(metas))
	canonical := make(map[string]bool, len(metas))
	for _, meta := range metas {
		if !Eligible(meta) {
			continue
		}
		meta.InvocationState = meta.EffectiveInvocationState()
		functions = append(functions, Function{
			Name:             meta.Type,
			NodeType:         meta.Type,
			IdentityRequired: meta.InvocationIdentityRequired(),
			Meta:             meta,
		})
		canonical[meta.Type] = true
	}
	sort.Slice(functions, func(i, j int) bool { return functions[i].Name < functions[j].Name })

	candidates := make([]string, len(functions))
	counts := make(map[string]int, len(functions))
	for i := range functions {
		alias := lowerCamel(functions[i].Name)
		if alias == "" || alias == "call" || canonical[alias] || !validIdentifier(alias) {
			continue
		}
		candidates[i] = alias
		counts[alias]++
	}
	for i, alias := range candidates {
		if alias != "" && counts[alias] == 1 {
			functions[i].Aliases = []string{alias}
		}
	}
	return functions
}

// NewCatalog derives a versioned catalog and hashes its deterministic JSON
// content, excluding the revision field itself. Revision is empty only when
// node metadata violates the public JSON contract and cannot be marshaled.
func NewCatalog(metas []flow.NodeMeta) Catalog {
	catalog := Catalog{SchemaVersion: SchemaVersion, Functions: Functions(metas)}
	ordered := struct {
		SchemaVersion int        `json:"schema_version"`
		Functions     []Function `json:"functions"`
	}{
		SchemaVersion: catalog.SchemaVersion,
		Functions:     catalog.Functions,
	}
	raw, err := json.Marshal(ordered)
	if err != nil {
		return catalog
	}
	digest := sha256.Sum256(raw)
	catalog.Revision = hex.EncodeToString(digest[:])
	return catalog
}

// Eligible reports whether a node has meaningful synchronous single-invocation
// behavior and a contract that can cross the script JSON/base64 boundary.
func Eligible(meta flow.NodeMeta) bool {
	if meta.Type == "" || meta.IsTrigger() ||
		meta.Category == flow.CategoryAnnotation || meta.Category == flow.CategoryPort ||
		flow.IsLinkNode(meta.Type) {
		return false
	}
	switch meta.Type {
	case flow.NodeTypeSubflow, flow.NodeTypeSubflowInput, flow.NodeTypeSubflowOutput,
		flow.NodeTypeTool, flow.NodeTypeScript, flow.NodeTypeManual, "join", "batch", "aggregate":
		return false
	}
	for _, port := range meta.Inputs {
		if port.Kind == flow.PortKindReturn || !portPortable(port) {
			return false
		}
	}
	for _, port := range meta.Outputs {
		if port.Kind == flow.PortKindParam {
			return false
		}
		if port.Kind != flow.PortKindError && !portPortable(port) {
			return false
		}
	}
	for _, field := range meta.ConfigSpec {
		if field.Port != "" && !portableType(field.Port) {
			return false
		}
	}
	return true
}

func portPortable(port flow.PortSpec) bool {
	if !portableType(port.Type) {
		return false
	}
	if port.TypeRule == nil {
		return true
	}
	if port.TypeRule.Default != "" && !portableType(port.TypeRule.Default) {
		return false
	}
	for _, rule := range port.TypeRule.Cases {
		if rule.Type != "" && !portableType(rule.Type) {
			return false
		}
	}
	return true
}

func portableType(portType string) bool {
	switch portType {
	case "", flow.PortAny, flow.PortBool, flow.PortNumber, flow.PortInteger,
		flow.PortFloat, flow.PortString, flow.PortObject, flow.PortBinary, flow.PortImage:
		return true
	}
	def, ok := flow.PortTypeFor(portType)
	return ok && def.Script
}

func lowerCamel(nodeType string) string {
	words := aliasWords(nodeType)
	if len(words) == 0 {
		return ""
	}
	var out strings.Builder
	out.WriteString(strings.ToLower(words[0]))
	for _, word := range words[1:] {
		runes := []rune(strings.ToLower(word))
		if len(runes) == 0 {
			continue
		}
		runes[0] = unicode.ToUpper(runes[0])
		out.WriteString(string(runes))
	}
	return out.String()
}

func aliasWords(nodeType string) []string {
	runes := []rune(nodeType)
	var words []string
	start := -1
	flush := func(end int) {
		if start >= 0 && end > start {
			words = append(words, string(runes[start:end]))
		}
		start = -1
	}
	for i, r := range runes {
		if !unicode.IsLetter(r) && !unicode.IsDigit(r) {
			flush(i)
			continue
		}
		if start < 0 {
			start = i
			continue
		}
		if !unicode.IsUpper(r) {
			continue
		}
		previous := runes[i-1]
		nextIsLower := i+1 < len(runes) && unicode.IsLower(runes[i+1])
		if unicode.IsLower(previous) || unicode.IsDigit(previous) ||
			(unicode.IsUpper(previous) && nextIsLower) {
			flush(i)
			start = i
		}
	}
	flush(len(runes))
	return words
}

func validIdentifier(value string) bool {
	for i, r := range value {
		if i == 0 && !unicode.IsLetter(r) && r != '_' && r != '$' {
			return false
		}
		if i > 0 && !unicode.IsLetter(r) && !unicode.IsDigit(r) && r != '_' && r != '$' {
			return false
		}
	}
	return value != ""
}
