package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/RainLib/open-review-platform/internal/domain"
	"github.com/google/uuid"
)

const usage = `Open Review CLI

Usage:
  openreview review create   [flags]
  openreview review status   --run <id> [flags]
  openreview review evidence --run <id> [flags]
  openreview review cancel   --run <id> --revision <n> [flags]

Common environment:
  OPEN_REVIEW_API_URL   Control-plane URL (for example https://review.example.com)
  OPEN_REVIEW_TENANT    Workspace slug
  OPEN_REVIEW_API_KEY   API key; never pass it as a command-line flag
`

type commandEnvironment struct {
	getenv func(string) string
	client *http.Client
	out    io.Writer
	errOut io.Writer
}

func main() {
	environment := commandEnvironment{
		getenv: os.Getenv,
		client: &http.Client{Timeout: 30 * time.Second},
		out:    os.Stdout, errOut: os.Stderr,
	}
	os.Exit(run(context.Background(), environment, os.Args[1:]))
}

func run(ctx context.Context, environment commandEnvironment, args []string) int {
	if len(args) < 2 || args[0] != "review" {
		fmt.Fprint(environment.errOut, usage)
		return 2
	}
	configuration, remaining, err := parseCommand(environment, args[1], args[2:])
	if err != nil {
		fmt.Fprintf(environment.errOut, "error: %v\n", err)
		return 2
	}
	if len(remaining) != 0 {
		fmt.Fprintf(environment.errOut, "error: unexpected arguments: %s\n", strings.Join(remaining, " "))
		return 2
	}
	if err := execute(ctx, environment, args[1], configuration); err != nil {
		fmt.Fprintf(environment.errOut, "error: %v\n", err)
		return 1
	}
	return 0
}

type commandConfiguration struct {
	baseURL, tenant, apiKey, runID, idempotencyKey string
	installationID, repository, baseRef, baseSHA   string
	headRef, headSHA, mode                         string
	reviewNumber, revision                         int
}

func parseCommand(environment commandEnvironment, command string, args []string) (commandConfiguration, []string, error) {
	configuration := commandConfiguration{
		baseURL: environment.getenv("OPEN_REVIEW_API_URL"),
		tenant:  environment.getenv("OPEN_REVIEW_TENANT"),
		apiKey:  environment.getenv("OPEN_REVIEW_API_KEY"),
		mode:    string(domain.ReviewModeConfigured),
	}
	flags := flag.NewFlagSet("review "+command, flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	flags.StringVar(&configuration.baseURL, "server", configuration.baseURL, "control-plane URL")
	flags.StringVar(&configuration.tenant, "tenant", configuration.tenant, "workspace slug")
	switch command {
	case "create":
		flags.StringVar(&configuration.installationID, "installation", "", "provider installation UUID")
		flags.StringVar(&configuration.repository, "repository", "", "repository owner/name")
		flags.IntVar(&configuration.reviewNumber, "number", 0, "pull or merge request number")
		flags.StringVar(&configuration.baseRef, "base-ref", "", "base Git ref")
		flags.StringVar(&configuration.baseSHA, "base-sha", "", "exact base commit SHA")
		flags.StringVar(&configuration.headRef, "head-ref", "", "head Git ref")
		flags.StringVar(&configuration.headSHA, "head-sha", "", "exact head commit SHA")
		flags.StringVar(&configuration.mode, "mode", configuration.mode, "configured, standard, deep, or security")
		flags.StringVar(&configuration.idempotencyKey, "idempotency-key", "", "stable retry key")
	case "status", "evidence":
		flags.StringVar(&configuration.runID, "run", "", "review run UUID")
	case "cancel":
		flags.StringVar(&configuration.runID, "run", "", "review run UUID")
		flags.IntVar(&configuration.revision, "revision", 0, "expected run revision")
	default:
		return commandConfiguration{}, nil, fmt.Errorf("unknown review command %q", command)
	}
	if err := flags.Parse(args); err != nil {
		return commandConfiguration{}, nil, err
	}
	configuration.baseURL = strings.TrimSuffix(strings.TrimSpace(configuration.baseURL), "/")
	configuration.tenant = strings.TrimSpace(configuration.tenant)
	configuration.apiKey = strings.TrimSpace(configuration.apiKey)
	if err := validateConnectionConfiguration(configuration); err != nil {
		return commandConfiguration{}, nil, err
	}
	return configuration, flags.Args(), nil
}

func validateConnectionConfiguration(configuration commandConfiguration) error {
	parsed, err := url.Parse(configuration.baseURL)
	if err != nil || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return errors.New("OPEN_REVIEW_API_URL or --server must be a valid base URL")
	}
	if parsed.Scheme != "https" && !(parsed.Scheme == "http" && (parsed.Hostname() == "127.0.0.1" || parsed.Hostname() == "localhost" || parsed.Hostname() == "::1")) {
		return errors.New("the control plane must use HTTPS except on localhost")
	}
	if configuration.tenant == "" || strings.Contains(configuration.tenant, "/") {
		return errors.New("OPEN_REVIEW_TENANT or --tenant is required")
	}
	if configuration.apiKey == "" {
		return errors.New("OPEN_REVIEW_API_KEY is required")
	}
	return nil
}

func execute(ctx context.Context, environment commandEnvironment, command string, configuration commandConfiguration) error {
	basePath := "/v1/tenants/" + url.PathEscape(configuration.tenant) + "/cli-reviews"
	method, endpoint := http.MethodGet, basePath
	var body any
	requestHeaders := map[string]string{}
	switch command {
	case "create":
		installationID, err := uuid.Parse(configuration.installationID)
		if err != nil {
			return errors.New("--installation must be a UUID")
		}
		input := domain.CLIReviewInput{
			InstallationID: installationID, Repository: configuration.repository, ReviewNumber: configuration.reviewNumber,
			BaseRef: configuration.baseRef, BaseSHA: configuration.baseSHA, HeadRef: configuration.headRef, HeadSHA: configuration.headSHA,
			Mode: domain.ReviewMode(configuration.mode),
		}
		idempotencyKey := configuration.idempotencyKey
		if idempotencyKey == "" {
			digest := sha256.Sum256([]byte(configuration.tenant + "\x00" + configuration.repository + "\x00" + strconv.Itoa(configuration.reviewNumber) + "\x00" + configuration.headSHA + "\x00" + configuration.mode))
			idempotencyKey = "cli:" + hex.EncodeToString(digest[:])
		}
		if _, valid := domain.NormalizeCLIReviewInput(input, idempotencyKey); !valid {
			return errors.New("review input is invalid; use an existing PR/MR, full 40/64-character SHAs, safe refs, and a supported mode")
		}
		method, body = http.MethodPost, input
		requestHeaders["Idempotency-Key"] = idempotencyKey
	case "status", "evidence", "cancel":
		if _, err := uuid.Parse(configuration.runID); err != nil {
			return errors.New("--run must be a UUID")
		}
		endpoint += "/" + url.PathEscape(configuration.runID)
		if command == "evidence" {
			endpoint += "/evidence"
		}
		if command == "cancel" {
			if configuration.revision < 1 {
				return errors.New("--revision must be greater than zero")
			}
			method, endpoint, body = http.MethodPost, endpoint+"/cancel", map[string]int{"revision": configuration.revision}
		}
	}
	var encoded io.Reader
	if body != nil {
		payload, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("encode request: %w", err)
		}
		encoded = bytes.NewReader(payload)
	}
	request, err := http.NewRequestWithContext(ctx, method, configuration.baseURL+endpoint, encoded)
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	request.Header.Set("Authorization", "Bearer "+configuration.apiKey)
	request.Header.Set("Accept", "application/json")
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	for key, value := range requestHeaders {
		request.Header.Set(key, value)
	}
	response, err := environment.client.Do(request)
	if err != nil {
		return fmt.Errorf("request control plane: %w", err)
	}
	defer response.Body.Close()
	responseBody, err := io.ReadAll(io.LimitReader(response.Body, 4<<20))
	if err != nil {
		return fmt.Errorf("read control-plane response: %w", err)
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return fmt.Errorf("control plane returned %s: %s", response.Status, strings.TrimSpace(string(responseBody)))
	}
	var pretty bytes.Buffer
	if json.Indent(&pretty, responseBody, "", "  ") == nil {
		_, _ = fmt.Fprintln(environment.out, pretty.String())
	} else {
		_, _ = environment.out.Write(responseBody)
		_, _ = fmt.Fprintln(environment.out)
	}
	return nil
}
