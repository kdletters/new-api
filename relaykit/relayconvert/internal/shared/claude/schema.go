package claude

import (
	"encoding/json"
	"slices"
	"strings"

	kitutil "github.com/QuantumNous/new-api/relaykit/relayconvert/kitutil"
	"github.com/tidwall/sjson"
)

// FunctionParametersToInputSchema returns function parameters as a Claude
// input_schema, adding type and properties when they are missing or null.
// Otherwise the client's bytes are kept, so the properties keep their order.
// ok is false when parameters are not a JSON object; the result is then an
// empty object schema.
func FunctionParametersToInputSchema(parameters json.RawMessage) (schema json.RawMessage, ok bool) {
	empty := json.RawMessage(`{"type":"object","properties":{}}`)
	switch kitutil.GetJsonType(parameters) {
	case "unknown", "null":
		return empty, true
	case "object":
	default:
		return empty, false
	}
	var fields struct {
		Type       json.RawMessage `json:"type"`
		Properties json.RawMessage `json:"properties"`
	}
	if err := kitutil.Unmarshal(parameters, &fields); err != nil {
		return empty, false
	}
	schema = parameters
	var err error
	if len(fields.Type) == 0 || kitutil.GetJsonType(fields.Type) == "null" {
		if schema, err = sjson.SetRawBytes(schema, "type", []byte(`"object"`)); err != nil {
			return empty, false
		}
	}
	if len(fields.Properties) == 0 || kitutil.GetJsonType(fields.Properties) == "null" {
		if schema, err = sjson.SetRawBytes(schema, "properties", []byte(`{}`)); err != nil {
			return empty, false
		}
	}
	return schema, true
}

// unionRule describes one JSON Schema union keyword that Claude rejects at the
// top level of a tool input_schema.
type unionRule struct {
	key string
	// allOf conjoins branch constraints, so a name required by any branch stays
	// required after flattening. anyOf/oneOf allow any single branch, so only
	// names required by every branch stay required.
	allOf bool
}

var claudeInputSchemaUnionRules = []unionRule{
	{key: "oneOf"},
	{key: "anyOf"},
	{key: "allOf", allOf: true},
}

// FlattenInputSchemaUnions removes a top-level oneOf/anyOf/allOf from a Claude
// tool input schema by merging the branch object properties into the top-level
// object, because Claude rejects top-level unions with
// "input_schema does not support oneOf, allOf, or anyOf at the top level".
//
// The rewrite is lossy: merged branch properties become optional unless every
// branch requires them, and branches that are not object schemas contribute
// nothing. The function reports whether the schema changed.
func FlattenInputSchemaUnions(schema map[string]any) bool {
	if schema == nil {
		return false
	}
	changed := false
	for _, rule := range claudeInputSchemaUnionRules {
		raw, exists := schema[rule.key]
		if !exists {
			continue
		}
		branches := objectBranches(raw)
		mergeBranchProperties(schema, branches)
		mergeRequired(schema, branches, rule.allOf)
		delete(schema, rule.key)
		changed = true
	}
	if changed {
		if _, exists := schema["type"]; !exists {
			schema["type"] = "object"
		}
		if _, exists := schema["properties"]; !exists {
			schema["properties"] = map[string]any{}
		}
	}
	return changed
}

// objectBranches returns the object-shaped branches of a union value. A branch
// that is not an object schema (non-map, or a string type other than "object")
// is dropped: the union still gets removed so the upstream no longer rejects the
// schema, and the flattened schema just becomes more permissive.
func objectBranches(raw any) []map[string]any {
	var items []any
	switch value := raw.(type) {
	case []any:
		items = value
	case []map[string]any:
		branches := make([]map[string]any, 0, len(value))
		for _, branch := range value {
			if branch != nil {
				branches = append(branches, branch)
			}
		}
		return branches
	default:
		return nil
	}
	branches := make([]map[string]any, 0, len(items))
	for _, item := range items {
		branch, ok := item.(map[string]any)
		if !ok || branch == nil {
			continue
		}
		if typeName, ok := branch["type"].(string); ok && !strings.EqualFold(strings.TrimSpace(typeName), "object") {
			continue
		}
		branches = append(branches, branch)
	}
	return branches
}

func mergeBranchProperties(schema map[string]any, branches []map[string]any) {
	for _, branch := range branches {
		branchProperties, ok := branch["properties"].(map[string]any)
		if !ok || len(branchProperties) == 0 {
			continue
		}
		top, ok := schema["properties"].(map[string]any)
		if !ok || top == nil {
			top = make(map[string]any, len(branchProperties))
			schema["properties"] = top
		}
		// Top-level properties win; earlier branches win over later ones.
		for name, property := range branchProperties {
			if _, exists := top[name]; !exists {
				top[name] = property
			}
		}
	}
}

func mergeRequired(schema map[string]any, branches []map[string]any, allOf bool) {
	if len(branches) == 0 {
		return
	}
	// Each branch's required list is a slice, not a set: iterating a map here
	// would make the merged order and therefore the emitted JSON unstable.
	branchRequired := make([][]string, 0, len(branches))
	for _, branch := range branches {
		branchRequired = append(branchRequired, requiredNameList(branch["required"]))
	}
	// Top-level required entries always survive; branch entries only add to them.
	merged := requiredNameList(schema["required"])
	if allOf {
		for _, names := range branchRequired {
			merged = appendMissing(merged, names)
		}
	} else {
		intersection := branchRequired[0]
		for _, names := range branchRequired[1:] {
			intersection = retainPresent(intersection, names)
		}
		merged = appendMissing(merged, intersection)
	}
	if len(merged) == 0 {
		delete(schema, "required")
		return
	}
	schema["required"] = merged
}

// requiredNameList preserves the declared order of a required list, dropping
// non-strings, blanks, and duplicates.
func requiredNameList(value any) []string {
	var items []any
	switch list := value.(type) {
	case []any:
		items = list
	case []string:
		items = make([]any, 0, len(list))
		for _, name := range list {
			items = append(items, name)
		}
	default:
		return nil
	}
	names := make([]string, 0, len(items))
	seen := make(map[string]struct{}, len(items))
	for _, item := range items {
		name, ok := item.(string)
		if !ok {
			continue
		}
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		if _, exists := seen[name]; exists {
			continue
		}
		seen[name] = struct{}{}
		names = append(names, name)
	}
	return names
}

func appendMissing(names, extra []string) []string {
	for _, name := range extra {
		if !slices.Contains(names, name) {
			names = append(names, name)
		}
	}
	return names
}

func retainPresent(names, required []string) []string {
	if len(names) == 0 {
		return nil
	}
	return slices.DeleteFunc(names, func(name string) bool {
		return !slices.Contains(required, name)
	})
}
