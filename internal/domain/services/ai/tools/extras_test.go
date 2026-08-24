package tools

import (
	"encoding/json"
	"testing"
)

func TestSendPollOptionsSchemaAcceptsStringAndArray(t *testing.T) {
	reg := NewRegistry()
	RegisterEngagementTools(reg)
	tool := reg.Get("send_poll")
	if tool == nil {
		t.Fatal("send_poll not registered")
	}

	raw, err := json.Marshal(tool.Parameters)
	if err != nil {
		t.Fatalf("marshal schema: %v", err)
	}
	var schema map[string]interface{}
	if err := json.Unmarshal(raw, &schema); err != nil {
		t.Fatalf("unmarshal schema: %v", err)
	}
	props, _ := schema["properties"].(map[string]interface{})
	options, _ := props["options"].(map[string]interface{})
	anyOf, _ := options["anyOf"].([]interface{})
	if len(anyOf) != 2 {
		t.Fatalf("options.anyOf want 2 variants, got %s", raw)
	}

	var sawString, sawArray bool
	for _, v := range anyOf {
		item, _ := v.(map[string]interface{})
		switch item["type"] {
		case "string":
			sawString = true
		case "array":
			sawArray = true
			if n, _ := item["minItems"].(float64); n != 2 {
				t.Errorf("array minItems = %v, want 2", item["minItems"])
			}
			if n, _ := item["maxItems"].(float64); n != 4 {
				t.Errorf("array maxItems = %v, want 4", item["maxItems"])
			}
		}
	}
	if !sawString || !sawArray {
		t.Fatalf("options.anyOf must include string and array, got %s", raw)
	}
}

func TestPollOptionsParsesStringAndArray(t *testing.T) {
	got := pollOptions("a trip, breathing room")
	if len(got) != 2 || got[0] != "a trip" || got[1] != "breathing room" {
		t.Fatalf("string options = %#v", got)
	}
	got = pollOptions([]interface{}{"a trip", "breathing room", "a thing I want"})
	if len(got) != 3 {
		t.Fatalf("array options = %#v", got)
	}
}
