package sso

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/RainLib/open-review-platform/internal/domain"
	"github.com/RainLib/open-review-platform/internal/store"
	"github.com/google/uuid"
)

const maxMetadataBytes = 1 << 20

type ProbeClient struct {
	AllowPrivateNetworks bool
	HTTPClient           *http.Client
}

func (p ProbeClient) Probe(ctx context.Context, target domain.SSOProbeTarget) (string, string, string) {
	targetURL, err := probeURL(target)
	if err != nil {
		return "", "invalid_target", err.Error()
	}
	client := p.HTTPClient
	if client == nil {
		client = safeHTTPClient(p.AllowPrivateNetworks)
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, targetURL.String(), nil)
	if err != nil {
		return "", "invalid_target", err.Error()
	}
	request.Header.Set("Accept", "application/json, application/samlmetadata+xml, application/xml, text/xml")
	response, err := client.Do(request)
	if err != nil {
		return "", "unreachable", "Identity provider metadata could not be reached."
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode > 299 {
		return "", "unexpected_status", fmt.Sprintf("Identity provider metadata returned HTTP %d.", response.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, maxMetadataBytes+1))
	if err != nil {
		return "", "read_failed", "Identity provider metadata could not be read."
	}
	if len(body) == 0 || len(body) > maxMetadataBytes {
		return "", "invalid_size", "Identity provider metadata is empty or exceeds 1 MiB."
	}
	if target.Protocol == domain.SSOProtocolOIDC {
		if err := validateOIDCMetadata(body, target); err != nil {
			return "", "invalid_metadata", err.Error()
		}
	} else if target.Protocol == domain.SSOProtocolSAML {
		if err := validateSAMLMetadata(body); err != nil {
			return "", "invalid_metadata", err.Error()
		}
	} else {
		return "", "invalid_protocol", "The configured SSO protocol is unsupported."
	}
	digest := sha256.Sum256(body)
	return hex.EncodeToString(digest[:]), "", ""
}

func probeURL(target domain.SSOProbeTarget) (*url.URL, error) {
	value := target.TargetURL
	if target.Protocol == domain.SSOProtocolOIDC {
		value = strings.TrimRight(value, "/") + "/.well-known/openid-configuration"
	}
	parsed, err := url.Parse(value)
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.User != nil || parsed.Fragment != "" {
		return nil, fmt.Errorf("identity provider metadata URL must be HTTPS")
	}
	return parsed, nil
}

func validateOIDCMetadata(body []byte, target domain.SSOProbeTarget) error {
	var metadata struct {
		Issuer                string `json:"issuer"`
		AuthorizationEndpoint string `json:"authorization_endpoint"`
		TokenEndpoint         string `json:"token_endpoint"`
		JWKSURI               string `json:"jwks_uri"`
	}
	if err := json.Unmarshal(body, &metadata); err != nil {
		return fmt.Errorf("OIDC discovery metadata is not valid JSON")
	}
	if strings.TrimRight(metadata.Issuer, "/") != strings.TrimRight(target.TargetURL, "/") {
		return fmt.Errorf("OIDC discovery issuer does not match the configured issuer")
	}
	for _, value := range []string{metadata.AuthorizationEndpoint, metadata.TokenEndpoint, metadata.JWKSURI} {
		parsed, err := url.Parse(value)
		if err != nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.User != nil {
			return fmt.Errorf("OIDC discovery metadata is missing a secure endpoint")
		}
	}
	return nil
}

func validateSAMLMetadata(body []byte) error {
	var metadata struct {
		XMLName xml.Name
		IDP     []struct {
			Services []struct {
				Location string `xml:"Location,attr"`
			} `xml:"SingleSignOnService"`
		} `xml:"IDPSSODescriptor"`
	}
	if err := xml.Unmarshal(body, &metadata); err != nil {
		return fmt.Errorf("SAML metadata is not valid XML")
	}
	if metadata.XMLName.Local != "EntityDescriptor" || len(metadata.IDP) == 0 {
		return fmt.Errorf("SAML metadata has no identity-provider descriptor")
	}
	for _, descriptor := range metadata.IDP {
		for _, service := range descriptor.Services {
			parsed, err := url.Parse(service.Location)
			if err == nil && parsed.Scheme == "https" && parsed.Host != "" {
				return nil
			}
		}
	}
	return fmt.Errorf("SAML metadata has no secure single-sign-on service")
}

func safeHTTPClient(allowPrivate bool) *http.Client {
	dialer := &net.Dialer{Timeout: 5 * time.Second, KeepAlive: 30 * time.Second}
	transport := &http.Transport{
		DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
			host, port, err := net.SplitHostPort(address)
			if err != nil {
				return nil, err
			}
			addresses, err := net.DefaultResolver.LookupIPAddr(ctx, host)
			if err != nil {
				return nil, err
			}
			for _, resolved := range addresses {
				if !allowPrivate && privateAddress(resolved.IP) {
					continue
				}
				return dialer.DialContext(ctx, network, net.JoinHostPort(resolved.IP.String(), port))
			}
			return nil, fmt.Errorf("identity provider resolves only to private or unsafe addresses")
		},
		ForceAttemptHTTP2: true,
	}
	return &http.Client{
		Transport: transport,
		Timeout:   10 * time.Second,
		CheckRedirect: func(request *http.Request, via []*http.Request) error {
			if len(via) >= 3 {
				return errors.New("too many identity provider redirects")
			}
			if request.URL.Scheme != "https" || request.URL.User != nil {
				return errors.New("identity provider redirect is not secure")
			}
			return nil
		},
	}
}

func privateAddress(ip net.IP) bool {
	return ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsUnspecified()
}

type ProbeStore interface {
	ClaimSSOProbe(context.Context, string) (*domain.SSOProbeTarget, error)
	CompleteSSOProbe(context.Context, uuid.UUID, string, string, string, string) error
}

type Processor struct {
	Store       ProbeStore
	Client      ProbeClient
	WorkerID    string
	TaskStarted func() func()
}

func (p Processor) RunOnce(ctx context.Context) (bool, error) {
	if p.Store == nil || strings.TrimSpace(p.WorkerID) == "" {
		return false, fmt.Errorf("SSO probe processor is not configured")
	}
	target, err := p.Store.ClaimSSOProbe(ctx, p.WorkerID)
	if errors.Is(err, store.ErrNoQueuedSSOProbe) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	done := func() {}
	if p.TaskStarted != nil {
		if release := p.TaskStarted(); release != nil {
			done = release
		}
	}
	defer done()
	probeCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	hash, errorCode, errorMessage := p.Client.Probe(probeCtx, *target)
	if err := p.Store.CompleteSSOProbe(ctx, target.ReceiptID, p.WorkerID, hash, errorCode, errorMessage); err != nil {
		return true, err
	}
	return true, nil
}
