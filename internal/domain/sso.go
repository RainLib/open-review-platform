package domain

import (
	"net"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"
)

type SSOProtocol string
type SSOConfigurationState string
type SSOProbeState string

const (
	SSOProtocolOIDC SSOProtocol = "oidc"
	SSOProtocolSAML SSOProtocol = "saml"

	SSOStateDraftSaved SSOConfigurationState = "draft_saved"
	SSOStateTesting    SSOConfigurationState = "testing"
	SSOStateVerified   SSOConfigurationState = "verified"
	SSOStateReady      SSOConfigurationState = "enforcement_ready"
	SSOStateEnforced   SSOConfigurationState = "enforced"
	SSOStateSuspended  SSOConfigurationState = "suspended"
	SSOProbeQueued     SSOProbeState         = "queued"
	SSOProbeRunning    SSOProbeState         = "running"
	SSOProbeSucceeded  SSOProbeState         = "succeeded"
	SSOProbeFailed     SSOProbeState         = "failed"
)

type SSOConfiguration struct {
	TenantID         uuid.UUID             `json:"tenant_id"`
	Protocol         SSOProtocol           `json:"protocol"`
	DisplayName      string                `json:"display_name"`
	IssuerURL        string                `json:"issuer_url,omitempty"`
	MetadataURL      string                `json:"metadata_url,omitempty"`
	ClientID         string                `json:"client_id,omitempty"`
	SecretConfigured bool                  `json:"secret_configured"`
	GroupClaim       string                `json:"group_claim"`
	State            SSOConfigurationState `json:"state"`
	Revision         int                   `json:"revision"`
	TestedRevision   *int                  `json:"tested_revision,omitempty"`
	BreakGlass       string                `json:"break_glass_subject,omitempty"`
	EnforcedAt       *time.Time            `json:"enforced_at,omitempty"`
	UpdatedBy        string                `json:"updated_by"`
	CreatedAt        time.Time             `json:"created_at"`
	UpdatedAt        time.Time             `json:"updated_at"`
}

type SSOConfigurationInput struct {
	Protocol         SSOProtocol `json:"protocol"`
	DisplayName      string      `json:"display_name"`
	IssuerURL        string      `json:"issuer_url,omitempty"`
	MetadataURL      string      `json:"metadata_url,omitempty"`
	ClientID         string      `json:"client_id,omitempty"`
	SecretRef        string      `json:"secret_ref,omitempty"`
	KeepSecret       bool        `json:"keep_secret,omitempty"`
	GroupClaim       string      `json:"group_claim,omitempty"`
	ExpectedRevision int         `json:"expected_revision"`
}

type SSOProbeReceipt struct {
	ID             uuid.UUID     `json:"id"`
	ConfigRevision int           `json:"config_revision"`
	Protocol       SSOProtocol   `json:"protocol"`
	TargetURL      string        `json:"target_url"`
	State          SSOProbeState `json:"state"`
	Attempt        int           `json:"attempt"`
	MetadataSHA256 string        `json:"metadata_sha256,omitempty"`
	ErrorCode      string        `json:"error_code,omitempty"`
	ErrorMessage   string        `json:"error_message,omitempty"`
	RequestedBy    string        `json:"requested_by"`
	CreatedAt      time.Time     `json:"created_at"`
	StartedAt      *time.Time    `json:"started_at,omitempty"`
	FinishedAt     *time.Time    `json:"finished_at,omitempty"`
}

type SSOProbeTarget struct {
	ReceiptID        uuid.UUID
	TenantID         uuid.UUID
	ConfigRevision   int
	Protocol         SSOProtocol
	TargetURL        string
	ExpectedClientID string
}

type SSODomain struct {
	ID             uuid.UUID  `json:"id"`
	Domain         string     `json:"domain"`
	ChallengeToken string     `json:"challenge_token"`
	State          string     `json:"state"`
	Revision       int        `json:"revision"`
	CreatedBy      string     `json:"created_by"`
	CreatedAt      time.Time  `json:"created_at"`
	VerifiedAt     *time.Time `json:"verified_at,omitempty"`
}

type SSORoleMapping struct {
	ID              uuid.UUID `json:"id"`
	GroupValue      string    `json:"group_value"`
	Role            string    `json:"role"`
	RepositoryScope string    `json:"repository_scope"`
	Revision        int       `json:"revision"`
	CreatedBy       string    `json:"created_by"`
	CreatedAt       time.Time `json:"created_at"`
	UpdatedAt       time.Time `json:"updated_at"`
}

type SSORoleMappingInput struct {
	GroupValue      string `json:"group_value"`
	Role            string `json:"role"`
	RepositoryScope string `json:"repository_scope"`
}

type SSOReadiness struct {
	Ready    bool     `json:"ready"`
	Blockers []string `json:"blockers"`
}

type SSOOverview struct {
	Configuration *SSOConfiguration `json:"configuration,omitempty"`
	Domains       []SSODomain       `json:"domains"`
	Mappings      []SSORoleMapping  `json:"mappings"`
	Probes        []SSOProbeReceipt `json:"probes"`
	Readiness     SSOReadiness      `json:"readiness"`
}

type SSOEnforcementInput struct {
	ExpectedRevision  int    `json:"expected_revision"`
	BreakGlassSubject string `json:"break_glass_subject"`
}

var (
	ssoClaimPattern  = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_.:-]{0,79}$`)
	dnsLabelPattern  = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?$`)
	secretRefPattern = regexp.MustCompile(`^(secret://[A-Za-z0-9._/-]{3,180}|env://OPEN_REVIEW_SSO_SECRET_[A-Z0-9_]{1,80})$`)
)

func NormalizeSSOConfigurationInput(input SSOConfigurationInput) (SSOConfigurationInput, bool) {
	input.DisplayName = strings.TrimSpace(input.DisplayName)
	input.IssuerURL = strings.TrimRight(strings.TrimSpace(input.IssuerURL), "/")
	input.MetadataURL = strings.TrimSpace(input.MetadataURL)
	input.ClientID = strings.TrimSpace(input.ClientID)
	input.SecretRef = strings.TrimSpace(input.SecretRef)
	input.GroupClaim = strings.TrimSpace(input.GroupClaim)
	if input.GroupClaim == "" {
		input.GroupClaim = "groups"
	}
	if input.DisplayName == "" || len(input.DisplayName) > 100 || !ssoClaimPattern.MatchString(input.GroupClaim) || input.ExpectedRevision < 0 {
		return SSOConfigurationInput{}, false
	}
	if input.SecretRef != "" && !secretRefPattern.MatchString(input.SecretRef) {
		return SSOConfigurationInput{}, false
	}
	switch input.Protocol {
	case SSOProtocolOIDC:
		if input.ClientID == "" || len(input.ClientID) > 200 || !validSSOEndpoint(input.IssuerURL) || input.MetadataURL != "" {
			return SSOConfigurationInput{}, false
		}
	case SSOProtocolSAML:
		if !validSSOEndpoint(input.MetadataURL) || input.IssuerURL != "" || input.ClientID != "" {
			return SSOConfigurationInput{}, false
		}
	default:
		return SSOConfigurationInput{}, false
	}
	return input, true
}

func NormalizeSSODomain(value string) (string, bool) {
	value = strings.ToLower(strings.TrimSuffix(strings.TrimSpace(value), "."))
	if len(value) < 3 || len(value) > 253 || net.ParseIP(value) != nil {
		return "", false
	}
	labels := strings.Split(value, ".")
	if len(labels) < 2 {
		return "", false
	}
	for _, label := range labels {
		if !dnsLabelPattern.MatchString(label) {
			return "", false
		}
	}
	return value, true
}

func NormalizeSSORoleMappingInput(input SSORoleMappingInput) (SSORoleMappingInput, bool) {
	input.GroupValue = strings.TrimSpace(input.GroupValue)
	input.Role = strings.TrimSpace(input.Role)
	input.RepositoryScope = strings.TrimSpace(input.RepositoryScope)
	if input.RepositoryScope == "" {
		input.RepositoryScope = "*"
	}
	if input.GroupValue == "" || len(input.GroupValue) > 160 || len(input.RepositoryScope) > 240 || strings.ContainsAny(input.RepositoryScope, "\r\n\x00") {
		return SSORoleMappingInput{}, false
	}
	switch input.Role {
	case "admin", "rule_admin", "reviewer", "viewer", "billing_viewer":
	default:
		return SSORoleMappingInput{}, false
	}
	return input, true
}

func validSSOEndpoint(value string) bool {
	parsed, err := url.Parse(value)
	return err == nil && parsed.Scheme == "https" && parsed.Host != "" && parsed.User == nil && parsed.Fragment == ""
}
