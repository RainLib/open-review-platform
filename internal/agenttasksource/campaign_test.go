package agenttasksource

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"github.com/RainLib/open-review-platform/internal/domain"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestCampaignScanPinsEveryFileAndDistinguishesPartialCoverage(t *testing.T) {
	for _, scenario := range []string{"complete", "second_page", "truncated", "unreadable", "base_changed"} {
		t.Run(scenario, func(t *testing.T) {
			sha := strings.Repeat("a", 40)
			reads := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != "GET" || r.Header.Get("Authorization") != "Bearer fixture-read" {
					t.Error("scan attempted an unauthorized write")
				}
				var result any
				switch {
				case strings.HasSuffix(r.URL.Path, "/team/repo"):
					result = map[string]string{"default_branch": "main"}
				case strings.HasSuffix(r.URL.Path, "/branches/main"):
					actual := sha
					if scenario == "base_changed" {
						actual = strings.Repeat("b", 40)
					}
					result = map[string]any{"commit": map[string]string{"id": actual}}
				case strings.HasSuffix(r.URL.Path, "/repository/tree"):
					if r.URL.Query().Get("ref") != sha && scenario != "base_changed" {
						t.Error("tree was not pinned")
					}
					entries := []map[string]string{}
					if scenario == "second_page" && r.URL.Query().Get("page") == "1" {
						for i := 0; i < 100; i++ {
							entries = append(entries, map[string]string{"path": fmt.Sprintf("dir/%03d", i), "type": "tree", "mode": "040000"})
						}
					} else {
						entries = append(entries, map[string]string{"path": "README.md", "type": "blob", "mode": "100644"})
					}
					if scenario == "truncated" {
						for len(entries) < 100 {
							entries = append(entries, map[string]string{"path": "dir", "type": "tree", "mode": "040000"})
						}
					}
					result = entries
				case strings.HasSuffix(r.URL.Path, "/files/README.md"):
					reads++
					if r.URL.Query().Get("ref") != sha && scenario != "base_changed" {
						t.Error("file was not pinned")
					}
					if scenario == "unreadable" {
						w.WriteHeader(403)
						return
					}
					result = map[string]any{"file_path": "README.md", "encoding": "base64", "size": 8, "content": base64.StdEncoding.EncodeToString([]byte("old old\n"))}
				default:
					http.NotFound(w, r)
					return
				}
				_ = json.NewEncoder(w).Encode(result)
			}))
			defer server.Close()
			resolver := Resolver{Resolver: staticResolver("fixture-read"), AllowGitLabHTTP: true}
			target := domain.AgentCampaignTarget{Provider: domain.ProviderGitLab, APIBaseURL: server.URL + "/api/v4", Repository: "team/repo"}
			input := domain.AgentCampaignInput{Mode: "scan", Paths: []string{"README.md"}, Search: "old"}
			receipt, err := resolver.ScanCampaign(context.Background(), target, input)
			if scenario == "unreadable" {
				if err == nil || receipt.Complete {
					t.Fatal("unavailable file inferred as no match")
				}
				return
			}
			if scenario == "truncated" {
				if err != nil || receipt.Complete || receipt.ErrorCode != "tree_page_limit" {
					t.Fatalf("truncation: %+v %v", receipt, err)
				}
				return
			}
			if err != nil || !receipt.Complete || receipt.Matches != 2 || receipt.FilesScanned != 1 || len(receipt.Files) != 1 || reads != 1 {
				t.Fatalf("scan: %+v %v", receipt, err)
			}
			if scenario == "base_changed" {
				task := domain.AgentTask{Provider: target.Provider, APIBaseURL: target.APIBaseURL, Repository: target.Repository, SourceBaseRef: "main", SourceBaseSHA: sha}
				if err = resolver.VerifyCampaignBase(context.Background(), task, "fixture-read"); err == nil {
					t.Fatal("changed base did not require new scan")
				}
			}
		})
	}
}
