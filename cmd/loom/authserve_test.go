package main

import (
	"strings"
	"testing"
)

func TestValidateServeAuth(t *testing.T) {
	tests := []struct {
		name           string
		users          int
		token          string
		allowAnonymous bool
		prod           bool
		allowUnsafe    bool
		wantErr        bool
		wantSubstring  string
	}{
		{name: "no users no token", users: 0, wantErr: true},
		{name: "no users anonymous", users: 0, allowAnonymous: true},
		{name: "no users token", users: 0, token: "a-strong-token-value"},
		{name: "users no anonymous", users: 3},
		{name: "anonymous with users", users: 3, allowAnonymous: true, wantErr: true},
		{name: "anonymous in prod", users: 0, allowAnonymous: true, prod: true, wantErr: true, wantSubstring: "LOOM_ALLOW_ANONYMOUS_UNSAFE"},
		{name: "anonymous in prod with explicit override", users: 0, allowAnonymous: true, prod: true, allowUnsafe: true},
		{name: "override does not bypass existing users", users: 3, allowAnonymous: true, prod: true, allowUnsafe: true, wantErr: true},
		{name: "prod with users", users: 1, prod: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := validateServeAuth(tc.users, tc.token, tc.allowAnonymous, tc.prod, tc.allowUnsafe)
			if (err != nil) != tc.wantErr {
				t.Fatalf("validateServeAuth(%d,%q,%v,%v,%v) err=%v, wantErr=%v",
					tc.users, tc.token, tc.allowAnonymous, tc.prod, tc.allowUnsafe, err, tc.wantErr)
			}
			if tc.wantSubstring != "" && (err == nil || !strings.Contains(err.Error(), tc.wantSubstring)) {
				t.Fatalf("error = %v, want it to mention %q", err, tc.wantSubstring)
			}
		})
	}
}
