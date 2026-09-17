package config

import "testing"

func TestDevelopmentAuthIsRejectedOutsideDevelopment(t *testing.T) {
	t.Setenv("CONTROL_DATABASE_URL", "postgres://example")
	t.Setenv("ENVIRONMENT", "production")
	t.Setenv("AUTH_MODE", "development")
	if _, err := Load(); err == nil {
		t.Fatal("development authentication must not be accepted in production")
	}
}
