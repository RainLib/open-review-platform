package store

import (
	"testing"

	"github.com/RainLib/open-review-platform/internal/domain"
)

func TestRepositoryScopeAllows(t *testing.T) {
	tests := []struct {
		name       string
		scope      string
		repository string
		want       bool
	}{
		{name: "exact repository", scope: "platform/api", repository: "platform/api", want: true},
		{name: "selected allowlist", scope: "platform/api, platform/web", repository: "platform/web", want: true},
		{name: "group wildcard", scope: "platform/*", repository: "platform/api", want: true},
		{name: "nested group wildcard", scope: "platform/*", repository: "platform/tools/api", want: true},
		{name: "GitHub installation-wide scope", scope: domain.AllAuthorizedRepositoriesScope, repository: "platform/tools/api", want: true},
		{name: "different group", scope: "platform/*", repository: "other/api", want: false},
		{name: "prefix is not a group", scope: "platform/*", repository: "platform-api/service", want: false},
		{name: "empty scope", scope: "", repository: "platform/api", want: false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := repositoryScopeAllows(test.scope, test.repository); got != test.want {
				t.Fatalf("repositoryScopeAllows(%q, %q) = %t, want %t", test.scope, test.repository, got, test.want)
			}
		})
	}
}

func TestRepositoryScopesOverlap(t *testing.T) {
	tests := []struct {
		left  string
		right string
		want  bool
	}{
		{left: "platform/*", right: "platform/api", want: true},
		{left: "platform/api", right: "platform/*", want: true},
		{left: "platform/*", right: "platform/tools/*", want: true},
		{left: domain.AllAuthorizedRepositoriesScope, right: "platform/api", want: true},
		{left: "platform/api,platform/web", right: "platform/web", want: true},
		{left: "platform/api", right: "other/api", want: false},
		{left: "platform/*", right: "platform-api/service", want: false},
	}
	for _, test := range tests {
		if got := repositoryScopesOverlap(test.left, test.right); got != test.want {
			t.Fatalf("repositoryScopesOverlap(%q, %q) = %t, want %t", test.left, test.right, got, test.want)
		}
	}
}
