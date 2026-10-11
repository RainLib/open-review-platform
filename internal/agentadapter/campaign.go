package agentadapter

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/RainLib/open-review-platform/internal/domain"
)

// Check known write paths before issuing credentials or invoking the model.
// Glob scopes still require the existing per-file guard after coding.
func (pipeline Pipeline) checkCampaignDeploymentScope(submission Submission) error {
	b := submission.Task.Campaign
	if b == nil {
		return nil
	}
	if !b.Valid() {
		return fmt.Errorf("invalid campaign scope")
	}
	if b.Mode == "replace" {
		for _, file := range b.Files {
			if !pipeline.allowedPath(file.Path) {
				return &campaignDeploymentScopeFailure{}
			}
		}
	} else {
		for _, pattern := range b.Paths {
			if !strings.ContainsAny(pattern, "*?[") && !pipeline.allowedPath(pattern) {
				return &campaignDeploymentScopeFailure{}
			}
		}
	}
	return nil
}

func campaignFile(workspace, file string) (string, error) {
	full := filepath.Join(workspace, filepath.FromSlash(file))
	relative, err := filepath.Rel(workspace, full)
	if err != nil || strings.HasPrefix(relative, "..") || filepath.IsAbs(relative) {
		return "", fmt.Errorf("campaign path escapes workspace")
	}
	current := workspace
	for _, part := range strings.Split(filepath.FromSlash(file), string(os.PathSeparator)) {
		current = filepath.Join(current, part)
		info, err := os.Lstat(current)
		if err != nil {
			return "", err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return "", fmt.Errorf("campaign paths must not follow symlinks")
		}
	}
	return full, nil
}
func applyCampaignReplacement(workspace string, b domain.AgentCampaignBinding) error {
	if !b.Valid() || b.Mode != "replace" {
		return fmt.Errorf("invalid deterministic campaign binding")
	}
	// Validate the complete patch before writing any file.
	type update struct {
		file string
		raw  []byte
		mode os.FileMode
	}
	updates := []update{}
	for _, f := range b.Files {
		full, err := campaignFile(workspace, f.Path)
		if err != nil {
			return err
		}
		raw, err := os.ReadFile(full)
		if err != nil {
			return err
		}
		h := sha256.Sum256(raw)
		if hex.EncodeToString(h[:]) != f.SHA256 || strings.Count(string(raw), b.Search) != f.Matches || f.Matches < 1 {
			return fmt.Errorf("campaign before-content or match count changed for %s", f.Path)
		}
		if len(raw)+f.Matches*(len(b.Replacement)-len(b.Search)) > 500000 {
			return fmt.Errorf("campaign replacement exceeds bounded file size")
		}
		info, err := os.Stat(full)
		if err != nil {
			return err
		}
		updates = append(updates, update{full, []byte(strings.ReplaceAll(string(raw), b.Search, b.Replacement)), info.Mode().Perm()})
	}
	for _, u := range updates {
		if err := os.WriteFile(u.file, u.raw, u.mode); err != nil {
			return err
		}
	}
	return nil
}
func (pipeline Pipeline) validateCampaignFiles(ctx context.Context, workspace string, submission Submission, files []string) error {
	b := submission.Task.Campaign
	if b == nil {
		return nil
	}
	if !b.Valid() {
		return fmt.Errorf("invalid campaign scope")
	}
	frozen := map[string]domain.AgentCampaignFile{}
	for _, f := range b.Files {
		frozen[f.Path] = f
	}
	for _, file := range files {
		if !domain.CampaignPathAllowed(b.Paths, file) {
			return fmt.Errorf("campaign change exceeds approved path scope: %s", file)
		}
		if submission.Task.OriginKind == "campaign" && b.Mode == "replace" {
			f, exists := frozen[file]
			if !exists {
				return fmt.Errorf("replacement changed an unscanned file")
			}
			full, err := campaignFile(workspace, file)
			if err != nil {
				return err
			}
			raw, err := os.ReadFile(full)
			if err != nil {
				return err
			}
			original, err := pipeline.git(ctx, workspace, noGitCredential, "show", submission.Task.SourceBaseSHA+":"+file)
			if err != nil {
				return err
			}
			h := sha256.Sum256([]byte(original))
			if hex.EncodeToString(h[:]) != f.SHA256 || strings.Count(original, b.Search) != f.Matches || string(raw) != strings.ReplaceAll(original, b.Search, b.Replacement) {
				return fmt.Errorf("replacement patch differs from approved deterministic result")
			}

		}
	}
	return nil
}
