package paj

import (
	"encoding/json"
	"testing"
)

func TestFlexBoolDecoding(t *testing.T) {
	cases := []struct {
		raw  string
		want bool
	}{
		{`true`, true},
		{`false`, false},
		{`"true"`, true},
		{`"false"`, false},
		{`"True"`, true},
		{`"1"`, true},
		{`1`, true},
		{`0`, false},
		{`null`, false},
	}
	for _, tc := range cases {
		var v FlexBool
		if err := json.Unmarshal([]byte(tc.raw), &v); err != nil {
			t.Errorf("unmarshal %s: %v", tc.raw, err)
			continue
		}
		if bool(v) != tc.want {
			t.Errorf("FlexBool(%s) = %v, want %v", tc.raw, bool(v), tc.want)
		}
	}

	// The documented verify shape (string isActive) and the live shape (bool)
	// must both decode so neither wire form breaks verification.
	for _, raw := range []string{
		`{"recipient":"a@b.c","isActive":"true","expiresAt":"","token":"t"}`,
		`{"recipient":"a@b.c","isActive":true,"expiresAt":"","token":"t"}`,
	} {
		var resp VerifyResponse
		if err := json.Unmarshal([]byte(raw), &resp); err != nil {
			t.Errorf("verify response %s: %v", raw, err)
			continue
		}
		if !bool(resp.IsActive) || resp.Token != "t" {
			t.Errorf("verify response mismatch: %+v", resp)
		}
	}
}
