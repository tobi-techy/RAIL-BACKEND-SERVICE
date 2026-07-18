package tools

import (
	"context"
	"fmt"
	"sync"

	"github.com/google/uuid"
	"github.com/rail-service/rail_service/internal/domain/services/ai/core"
)

// Registry holds all registered tools and provides dispatch by name.
// Tools register themselves at startup with nil-checkable dependencies.
type Registry struct {
	mu    sync.RWMutex
	tools map[string]*core.Tool
}

// NewRegistry creates an empty tool registry.
func NewRegistry() *Registry {
	return &Registry{
		tools: make(map[string]*core.Tool),
	}
}

// Register adds a tool to the registry. Panics on name conflict.
func (r *Registry) Register(tool *core.Tool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.tools[tool.Name]; ok {
		panic(fmt.Sprintf("tool %s already registered", tool.Name))
	}
	r.tools[tool.Name] = tool
}

// RegisterIf adds a tool only when the condition is true (dependency available pattern).
func (r *Registry) RegisterIf(condition bool, tool *core.Tool) {
	if condition {
		r.Register(tool)
	}
}

// Get returns a tool by name, or nil if not found.
func (r *Registry) Get(name string) *core.Tool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.tools[name]
}

// GetAll returns all registered tools.
func (r *Registry) GetAll() []*core.Tool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	result := make([]*core.Tool, 0, len(r.tools))
	for _, t := range r.tools {
		result = append(result, t)
	}
	return result
}

// GetByCategory returns all tools in a given category.
func (r *Registry) GetByCategory(cat core.ToolCategory) []*core.Tool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	var result []*core.Tool
	for _, t := range r.tools {
		if t.Category == cat {
			result = append(result, t)
		}
	}
	return result
}

// GetByCategories returns all tools matching at least one of the given categories.
func (r *Registry) GetByCategories(cats []core.ToolCategory) []*core.Tool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	lookup := make(map[core.ToolCategory]bool, len(cats))
	for _, c := range cats {
		lookup[c] = true
	}
	var result []*core.Tool
	for _, t := range r.tools {
		if lookup[t.Category] {
			result = append(result, t)
		}
	}
	return result
}

// Execute dispatches a tool by name with the given arguments.
func (r *Registry) Execute(ctx context.Context, userID uuid.UUID, name string, args map[string]interface{}, deps *core.Dependencies) (*core.ToolResult, error) {
	tool := r.Get(name)
	if tool == nil {
		return nil, fmt.Errorf("unknown tool: %s", name)
	}
	return tool.Execute(ctx, userID, args, deps)
}

// ToInfrastructureTools converts the registered tools to the old ai.Tool format
// for LLM provider compatibility.
func (r *Registry) ToInfrastructureTools() []map[string]interface{} {
	tools := r.GetAll()
	result := make([]map[string]interface{}, len(tools))
	for i, t := range tools {
		result[i] = map[string]interface{}{
			"name":        t.Name,
			"description": t.Description,
			"parameters":  t.Parameters,
		}
	}
	return result
}

// Count returns the number of registered tools.
func (r *Registry) Count() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.tools)
}

// ToolNames returns all registered tool names.
func (r *Registry) ToolNames() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	names := make([]string, 0, len(r.tools))
	for name := range r.tools {
		names = append(names, name)
	}
	return names
}

// --- Tool Builders ---

// NewTool creates a ready-to-use Tool with an ID-prepending wrapper around the
// execute function so tools don't have to worry about user ID extraction.
func NewTool(name, description string, params map[string]interface{}, cat core.ToolCategory, fn func(ctx context.Context, userID uuid.UUID, args map[string]interface{}, deps *core.Dependencies) (*core.ToolResult, error)) *core.Tool {
	return &core.Tool{
		Name:        name,
		Description: description,
		Parameters:  params,
		Category:    cat,
		Execute:     fn,
	}
}

// SimpleArgs is a helper to build JSON Schema parameters for a simple string-keyed
// set of required and optional properties.
func SimpleArgs(properties map[string]map[string]interface{}, required []string) map[string]interface{} {
	return map[string]interface{}{
		"type":       "object",
		"properties": properties,
		"required":   required,
	}
}

// StringParam builds a JSON Schema string property.
func StringParam(description string) map[string]interface{} {
	return map[string]interface{}{
		"type":        "string",
		"description": description,
	}
}

// NumberParam builds a JSON Schema number property.
func NumberParam(description string) map[string]interface{} {
	return map[string]interface{}{
		"type":        "number",
		"description": description,
	}
}

// BoolParam builds a JSON Schema boolean property.
func BoolParam(description string) map[string]interface{} {
	return map[string]interface{}{
		"type":        "boolean",
		"description": description,
	}
}

// ArrayOfObjectsParam builds a JSON Schema array-of-objects property. The item
// shape is described in the description since callers pass free-form object rows.
func ArrayOfObjectsParam(description string) map[string]interface{} {
	return map[string]interface{}{
		"type":        "array",
		"description": description,
		"items":       map[string]interface{}{"type": "object"},
	}
}

// EnumParam builds a JSON Schema string property with allowed values.
func EnumParam(description string, options []string) map[string]interface{} {
	return map[string]interface{}{
		"type":        "string",
		"description": description,
		"enum":        options,
	}
}

// ---- Helper for old-style string args extraction ----

// GetArgString extracts a string argument from the arguments map.
func GetArgString(args map[string]interface{}, key string) string {
	if args == nil {
		return ""
	}
	v, ok := args[key]
	if !ok {
		return ""
	}
	s, ok := v.(string)
	if !ok {
		return fmt.Sprintf("%v", v)
	}
	return s
}

// GetArgFloat extracts a float64 argument from the arguments map.
func GetArgFloat(args map[string]interface{}, key string) float64 {
	if args == nil {
		return 0
	}
	v, ok := args[key]
	if !ok {
		return 0
	}
	switch n := v.(type) {
	case float64:
		return n
	case int:
		return float64(n)
	case int64:
		return float64(n)
	default:
		return 0
	}
}

// ValidateContext ensures the context has a userID and returns it.
func ValidateContext(ctx context.Context, userID uuid.UUID) error {
	if userID == uuid.Nil {
		return fmt.Errorf("userID is required")
	}
	return nil
}
