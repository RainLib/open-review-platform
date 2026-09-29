package agentadapter

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/RainLib/open-review-platform/internal/domain"
	"github.com/google/uuid"
)

func TestFileCredentialSourceMatchesOnlyExactInstallationAndRepository(t *testing.T) {
	installationID := uuid.New()
	otherID := uuid.New()
	path := filepath.Join(t.TempDir(), "credentials.json")
	secret := "repository-only-test-token"
	contents := `{"version":1,"entries":[` +
		`{"installation_id":"` + installationID.String() + `","provider":"gitlab","api_base_url":"https://gitlab.example/api/v4","repository":"team/service","clone_base_url":"https://gitlab.example","token":"` + secret + `"},` +
		`{"installation_id":"` + otherID.String() + `","provider":"gitlab","api_base_url":"https://gitlab.example/api/v4","repository":"team/service","clone_base_url":"https://gitlab.example","token":"other-token"}` +
		`]}`
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
	source := FileCredentialSource{Path: path}
	if err := source.Validate(); err != nil {
		t.Fatalf("valid credential file was rejected at startup: %v", err)
	}
	scope := RepositoryCredentialScope{InstallationID: installationID, Provider: domain.ProviderGitLab, APIBaseURL: "https://gitlab.example/api/v4", Repository: "team/service"}
	credential, err := source.Resolve(context.Background(), scope)
	if err != nil || credential.Token != secret || credential.CloneBaseURL != "https://gitlab.example" {
		t.Fatalf("exact scope was not resolved: token-matched=%t clone=%q err=%v", credential.Token == secret, credential.CloneBaseURL, err)
	}
	for _, changed := range []RepositoryCredentialScope{
		{InstallationID: uuid.New(), Provider: scope.Provider, APIBaseURL: scope.APIBaseURL, Repository: scope.Repository},
		{InstallationID: scope.InstallationID, Provider: domain.ProviderGitHub, APIBaseURL: scope.APIBaseURL, Repository: scope.Repository},
		{InstallationID: scope.InstallationID, Provider: scope.Provider, APIBaseURL: "https://another.example/api/v4", Repository: scope.Repository},
		{InstallationID: scope.InstallationID, Provider: scope.Provider, APIBaseURL: scope.APIBaseURL, Repository: "team/other"},
	} {
		if _, err := source.Resolve(context.Background(), changed); err == nil || strings.Contains(err.Error(), secret) {
			t.Fatalf("wrong scope resolved or leaked a token: %+v err=%v", changed, err)
		}
	}
}

func TestFileCredentialSourceRejectsBroadPermissionsSymlinksAndDuplicates(t *testing.T) {
	installationID := uuid.New()
	path := filepath.Join(t.TempDir(), "credentials.json")
	entry := `{"installation_id":"` + installationID.String() + `","provider":"github","api_base_url":"https://api.github.com","repository":"acme/service","clone_base_url":"https://github.com","token":"secret-test-token"}`
	scope := RepositoryCredentialScope{InstallationID: installationID, Provider: domain.ProviderGitHub, APIBaseURL: "https://api.github.com", Repository: "acme/service"}
	if err := os.WriteFile(path, []byte(`{"version":1,"entries":[`+entry+`]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := (FileCredentialSource{Path: path}).Resolve(context.Background(), scope); err == nil {
		t.Fatal("world-readable adapter credential file was accepted")
	}
	if err := (FileCredentialSource{Path: path}).Validate(); err == nil {
		t.Fatal("world-readable adapter credential file passed startup validation")
	}
	if err := os.Chmod(path, 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(t.TempDir(), "credentials-link.json")
	if err := os.Symlink(path, link); err != nil {
		t.Fatal(err)
	}
	if _, err := (FileCredentialSource{Path: link}).Resolve(context.Background(), scope); err == nil {
		t.Fatal("symlinked credential file was accepted")
	}
	if err := (FileCredentialSource{Path: link}).Validate(); err == nil {
		t.Fatal("symlinked credential file passed startup validation")
	}
	if err := os.WriteFile(path, []byte(`{"version":1,"entries":[`+entry+`,`+entry+`]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := (FileCredentialSource{Path: path}).Resolve(context.Background(), scope); err == nil {
		t.Fatal("duplicate credential scope was accepted")
	}
	if err := (FileCredentialSource{Path: path}).Validate(); err == nil {
		t.Fatal("duplicate credential scope passed startup validation")
	}
}

func TestFileCredentialSourceValidateRejectsMalformedFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "credentials.json")
	if err := os.WriteFile(path, []byte(`{"version":1,"entries":[]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := (FileCredentialSource{Path: path}).Validate(); err == nil {
		t.Fatal("empty credential file passed startup validation")
	}
}

func TestFileCredentialSourceRejectsOversizedFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "credentials.json")
	if err := os.WriteFile(path, []byte(strings.Repeat("x", maxCredentialFileBytes+1)), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := (FileCredentialSource{Path: path}).Validate(); err == nil {
		t.Fatal("oversized credential file passed startup validation")
	}
}
