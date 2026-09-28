package main

import "testing"

func TestPublicGitLabBaseURLRequiresTrustedBrowserOrigin(t *testing.T) {
	for _, tc := range []struct {
		value     string
		allowHTTP bool
		want      bool
	}{
		{"https://gitlab.example.com/gitlab", false, true},
		{"http://127.0.0.1:8929", true, true},
		{"http://127.0.0.1:8929", false, false},
		{"http://gitlab:8929", true, false},
		{"https://user:password@gitlab.example.com", false, false},
		{"https://gitlab.example.com?next=evil", false, false},
	} {
		if got := validPublicDraftBaseURL(tc.value, tc.allowHTTP); got != tc.want {
			t.Errorf("base %q allowHTTP=%v: got %v, want %v", tc.value, tc.allowHTTP, got, tc.want)
		}
	}
}
