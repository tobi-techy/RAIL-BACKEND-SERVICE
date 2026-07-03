package ramphub

import "testing"

func TestExtractErrorMessage(t *testing.T) {
	cases := []struct {
		name string
		body string
		want string
	}{
		{"string message", `{"message":"Invalid bank code","error":"Bad Request"}`, "Invalid bank code"},
		{"array message", `{"message":["property name should not exist"],"error":"Bad Request"}`, "property name should not exist"},
		{"masks account number", `{"message":"Could not resolve account 8050201494"}`, "Could not resolve account ***"},
		{"no message field", `{"error":"Bad Request"}`, ""},
		{"invalid json", `not json`, ""},
	}
	for _, c := range cases {
		if got := extractErrorMessage([]byte(c.body)); got != c.want {
			t.Errorf("%s: extractErrorMessage = %q, want %q", c.name, got, c.want)
		}
	}
}
