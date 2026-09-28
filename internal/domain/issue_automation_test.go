package domain

import "testing"

func TestDefaultIssueAutoCreatePolicySerializesEmptyLists(t *testing.T) {
	policy := DefaultIssueAutoCreatePolicy()
	if policy.RepositoryScopes == nil || policy.Categories == nil || policy.Labels == nil {
		t.Fatal("default issue policy must expose empty lists, not null lists")
	}
	if len(policy.RepositoryScopes) != 0 || len(policy.Categories) != 0 || len(policy.Labels) != 0 {
		t.Fatal("default issue policy lists must start empty")
	}
}
