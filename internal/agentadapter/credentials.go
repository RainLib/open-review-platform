package agentadapter

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/RainLib/open-review-platform/internal/domain"
	"github.com/google/uuid"
	"golang.org/x/sys/unix"
)

// RepositoryCredentialScope is the signed, immutable identity of one
// installation and repository. It contains no secret or arbitrary token ref.
type RepositoryCredentialScope struct {
	AttemptID      uuid.UUID
	AdapterJobID   string
	InstallationID uuid.UUID
	Provider       domain.Provider
	APIBaseURL     string
	Repository     string
}

type RepositoryCredential struct {
	CloneBaseURL string
	Token        string
}

// RepositoryCredentialSource is adapter-owned. The control plane cannot
// choose a credential; it can only identify the already admitted installation.
type RepositoryCredentialSource interface {
	Resolve(context.Context, RepositoryCredentialScope) (RepositoryCredential, error)
}

// FileCredentialSource is a single-node/self-hosted credential source. The
// file is re-read for each task so rotation governs new executions. The file
// is adapter-only; it is never mounted into the per-job Docker child. The
// development UID executor remains unsafe for production write credentials.
type FileCredentialSource struct{ Path string }

const maxCredentialFileBytes = 64 << 10

func (source FileCredentialSource) Resolve(ctx context.Context, scope RepositoryCredentialScope) (RepositoryCredential, error) {
	if err := ctx.Err(); err != nil {
		return RepositoryCredential{}, err
	}
	if scope.InstallationID == uuid.Nil || !scope.Provider.Valid() || strings.TrimSpace(scope.APIBaseURL) == "" || strings.TrimSpace(scope.Repository) == "" {
		return RepositoryCredential{}, fmt.Errorf("adapter credential scope is invalid")
	}
	entries, err := source.load()
	if err != nil {
		return RepositoryCredential{}, err
	}
	for _, entry := range entries {
		id, _ := uuid.Parse(entry.InstallationID)
		if id == scope.InstallationID && entry.Provider == scope.Provider && entry.APIBaseURL == strings.TrimSuffix(strings.TrimSpace(scope.APIBaseURL), "/") && entry.Repository == strings.Trim(strings.TrimSpace(scope.Repository), "/") {
			return RepositoryCredential{CloneBaseURL: entry.CloneBaseURL, Token: entry.Token}, nil
		}
	}
	return RepositoryCredential{}, fmt.Errorf("no adapter credential matches the admitted installation and repository")
}

// Validate fails startup before an approved task can be queued against a
// missing or malformed credential file. Resolve still re-reads on every task
// to enforce rotations and revocations after startup.
func (source FileCredentialSource) Validate() error {
	_, err := source.load()
	return err
}

// CredentialMapScope exposes only the non-secret part of a private credential
// entry so provider-specific brokers can reject unusable mappings at startup.
type CredentialMapScope struct {
	InstallationID uuid.UUID
	Provider       domain.Provider
	APIBaseURL     string
	Repository     string
	CloneBaseURL   string
}

func (source FileCredentialSource) ValidateScopes(allowed func(CredentialMapScope) bool) error {
	if allowed == nil {
		return fmt.Errorf("adapter credential scope validator is missing")
	}
	entries, err := source.load()
	if err != nil {
		return err
	}
	for _, entry := range entries {
		id, _ := uuid.Parse(entry.InstallationID) // load already checked every ID
		if !allowed(CredentialMapScope{
			InstallationID: id, Provider: entry.Provider,
			APIBaseURL: entry.APIBaseURL, Repository: entry.Repository,
			CloneBaseURL: entry.CloneBaseURL,
		}) {
			return fmt.Errorf("adapter credential file has an unusable provider scope")
		}
	}
	return nil
}

type fileCredentialEntry struct {
	InstallationID string          `json:"installation_id"`
	Provider       domain.Provider `json:"provider"`
	APIBaseURL     string          `json:"api_base_url"`
	Repository     string          `json:"repository"`
	CloneBaseURL   string          `json:"clone_base_url"`
	Token          string          `json:"token"`
}

func (source FileCredentialSource) load() ([]fileCredentialEntry, error) {
	path := strings.TrimSpace(source.Path)
	if path == "" {
		return nil, fmt.Errorf("adapter credential file is not configured")
	}
	// O_NOFOLLOW and fstat bind the permission check to the exact file read.
	// A path swap between Lstat and ReadFile must not turn a private source
	// into a symlink or another readable file.
	fd, err := unix.Open(path, unix.O_RDONLY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
	if err != nil {
		return nil, fmt.Errorf("adapter credential file must be a private regular file")
	}
	file := os.NewFile(uintptr(fd), path)
	defer file.Close()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0o077 != 0 || info.Size() > maxCredentialFileBytes {
		return nil, fmt.Errorf("adapter credential file must be a private regular file")
	}
	contents, err := io.ReadAll(io.LimitReader(file, maxCredentialFileBytes+1))
	if err != nil || len(contents) > maxCredentialFileBytes {
		return nil, fmt.Errorf("adapter credential file cannot be read safely")
	}
	var document struct {
		Version int                   `json:"version"`
		Entries []fileCredentialEntry `json:"entries"`
	}
	decoder := json.NewDecoder(strings.NewReader(string(contents)))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&document) != nil || decoder.Decode(&struct{}{}) != io.EOF || document.Version != 1 || len(document.Entries) == 0 || len(document.Entries) > 100 {
		return nil, fmt.Errorf("adapter credential file format is invalid")
	}
	seen := make(map[string]bool, len(document.Entries))
	for index, entry := range document.Entries {
		id, parseErr := uuid.Parse(strings.TrimSpace(entry.InstallationID))
		apiBase := strings.TrimSuffix(strings.TrimSpace(entry.APIBaseURL), "/")
		repository := strings.Trim(strings.TrimSpace(entry.Repository), "/")
		cloneBase := strings.TrimSuffix(strings.TrimSpace(entry.CloneBaseURL), "/")
		if parseErr != nil || id == uuid.Nil || !entry.Provider.Valid() || apiBase == "" || repository == "" || cloneBase == "" || strings.TrimSpace(entry.Token) == "" {
			return nil, fmt.Errorf("adapter credential file has an invalid entry")
		}
		key := id.String() + "\x00" + string(entry.Provider) + "\x00" + apiBase + "\x00" + repository
		if seen[key] {
			return nil, fmt.Errorf("adapter credential file has a duplicate scope")
		}
		seen[key] = true
		document.Entries[index].InstallationID = id.String()
		document.Entries[index].APIBaseURL = apiBase
		document.Entries[index].Repository = repository
		document.Entries[index].CloneBaseURL = cloneBase
	}
	return document.Entries, nil
}
