package ai

import (
	"strings"
	"testing"
)

func TestBuildMemegenURL(t *testing.T) {
	tests := []struct {
		name   string
		tmpl   string
		top    string
		bottom string
		want   string
	}{
		{"basic", "drake", "spending it all", "stashing 30%", "https://api.memegen.link/images/drake/spending_it_all/stashing_30~p.png"},
		{"empty top", "pigeon", "", "is this a budget?", "https://api.memegen.link/images/pigeon/_/is_this_a_budget~q.png"},
		{"unicode naira", "fry", "₦80k on food", "", "https://api.memegen.link/images/fry/%E2%82%A680k_on_food/_.png"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := buildMemegenURL(tc.tmpl, tc.top, tc.bottom)
			if got != tc.want {
				t.Fatalf("buildMemegenURL(%q,%q,%q)\n got = %s\nwant = %s", tc.tmpl, tc.top, tc.bottom, got, tc.want)
			}
		})
	}
}

func TestSendMemeToolEnumMatchesCatalog(t *testing.T) {
	tool := SendMemeTool()
	props, _ := tool.Parameters["properties"].(map[string]interface{})
	tmplProp, _ := props["template"].(map[string]interface{})
	enum, _ := tmplProp["enum"].([]string)
	if len(enum) != len(memeTemplates) {
		t.Fatalf("enum length %d != catalog length %d", len(enum), len(memeTemplates))
	}
	for _, e := range memeTemplates {
		if !strings.Contains(tool.Description, e.ID) {
			t.Errorf("tool description missing template id %q", e.ID)
		}
	}
}
