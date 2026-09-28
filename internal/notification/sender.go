package notification

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/RainLib/open-review-platform/internal/domain"
)

type Credential struct {
	WebhookURL    string `json:"webhook_url"`
	SigningSecret string `json:"signing_secret,omitempty"`
}

type CredentialResolver interface {
	Resolve(context.Context, string) (Credential, error)
}

type EnvResolver struct{}

func (EnvResolver) Resolve(_ context.Context, ref string) (Credential, error) {
	if !strings.HasPrefix(ref, "env:") {
		return Credential{}, fmt.Errorf("notification secret ref must use env:NAME")
	}
	name := strings.TrimPrefix(ref, "env:")
	if name == "" {
		return Credential{}, fmt.Errorf("notification environment variable is required")
	}
	raw := strings.TrimSpace(os.Getenv(name))
	if raw == "" {
		return Credential{}, fmt.Errorf("notification credential %s is not configured", name)
	}
	credential := Credential{WebhookURL: raw}
	if strings.HasPrefix(raw, "{") {
		if err := json.Unmarshal([]byte(raw), &credential); err != nil {
			return Credential{}, fmt.Errorf("decode notification credential %s: %w", name, err)
		}
	}
	if err := validateWebhookURL(credential.WebhookURL); err != nil {
		return Credential{}, fmt.Errorf("notification credential %s: %w", name, err)
	}
	return credential, nil
}

type Sender struct {
	Client   *http.Client
	Resolver CredentialResolver
	Now      func() time.Time
}

func (s Sender) Send(ctx context.Context, delivery domain.NotificationDelivery) (int, error) {
	if s.Resolver == nil {
		return 0, fmt.Errorf("notification credential resolver is required")
	}
	credential, err := s.Resolver.Resolve(ctx, delivery.Destination.SecretRef)
	if err != nil {
		return 0, err
	}
	now := time.Now
	if s.Now != nil {
		now = s.Now
	}
	sentAt := now().UTC()
	body, endpoint, err := payload(delivery.Destination.Provider, credential, delivery.Event, sentAt)
	if err != nil {
		return 0, err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return 0, fmt.Errorf("create notification request: %w", err)
	}
	request.Header.Set("Content-Type", "application/json; charset=utf-8")
	request.Header.Set("User-Agent", "open-review-notifier/1")
	if delivery.Destination.Provider == domain.NotificationWebhook && credential.SigningSecret != "" {
		timestamp := strconv.FormatInt(sentAt.Unix(), 10)
		request.Header.Set("X-Open-Review-Timestamp", timestamp)
		request.Header.Set("X-Open-Review-Signature-256", "sha256="+hmacHex(
			[]byte(credential.SigningSecret),
			append([]byte(timestamp+"."), body...),
		))
	}
	client := s.Client
	if client == nil {
		client = &http.Client{Timeout: 15 * time.Second}
	}
	response, err := client.Do(request)
	if err != nil {
		// http.Client errors commonly include the full request URL. Robot webhook
		// query strings are credentials, so never return or persist that detail.
		return 0, fmt.Errorf("notification transport failed")
	}
	defer response.Body.Close()
	responseBody, readErr := io.ReadAll(io.LimitReader(response.Body, 64<<10))
	if readErr != nil {
		return response.StatusCode, fmt.Errorf("notification response could not be read")
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return response.StatusCode, fmt.Errorf("notification returned HTTP %d", response.StatusCode)
	}
	if err := validateProviderResponse(delivery.Destination.Provider, responseBody); err != nil {
		return response.StatusCode, err
	}
	return response.StatusCode, nil
}

func validateProviderResponse(provider domain.NotificationProvider, body []byte) error {
	switch provider {
	case domain.NotificationDingTalk:
		var result struct {
			ErrorCode *int `json:"errcode"`
		}
		if err := json.Unmarshal(body, &result); err != nil || result.ErrorCode == nil || *result.ErrorCode != 0 {
			return fmt.Errorf("dingtalk rejected notification")
		}
	case domain.NotificationFeishu:
		var result struct {
			Code       *int `json:"code"`
			StatusCode *int `json:"StatusCode"`
		}
		if err := json.Unmarshal(body, &result); err != nil {
			return fmt.Errorf("feishu rejected notification")
		}
		if (result.Code != nil && *result.Code != 0) || (result.StatusCode != nil && *result.StatusCode != 0) || (result.Code == nil && result.StatusCode == nil) {
			return fmt.Errorf("feishu rejected notification")
		}
	}
	return nil
}

func payload(provider domain.NotificationProvider, credential Credential, event domain.NotificationEvent, now time.Time) ([]byte, string, error) {
	title := notificationTitle(event)
	message := notificationMessage(event)
	endpoint := credential.WebhookURL
	var value any
	switch provider {
	case domain.NotificationDingTalk:
		if credential.SigningSecret != "" {
			timestamp := strconv.FormatInt(now.UnixMilli(), 10)
			signature := hmacBase64([]byte(credential.SigningSecret), []byte(timestamp+"\n"+credential.SigningSecret))
			parsed, _ := url.Parse(endpoint)
			query := parsed.Query()
			query.Set("timestamp", timestamp)
			query.Set("sign", signature)
			parsed.RawQuery = query.Encode()
			endpoint = parsed.String()
		}
		value = map[string]any{"msgtype": "markdown", "markdown": map[string]string{"title": title, "text": "### " + title + "\n\n" + message}}
	case domain.NotificationFeishu:
		value = map[string]any{"msg_type": "interactive", "card": map[string]any{"header": map[string]any{"title": map[string]string{"tag": "plain_text", "content": title}}, "elements": []any{map[string]any{"tag": "div", "text": map[string]string{"tag": "lark_md", "content": message}}}}}
		if credential.SigningSecret != "" {
			timestamp := strconv.FormatInt(now.Unix(), 10)
			value.(map[string]any)["timestamp"] = timestamp
			value.(map[string]any)["sign"] = hmacBase64([]byte(timestamp+"\n"+credential.SigningSecret), nil)
		}
	case domain.NotificationSlack:
		// Incoming webhooks accept a simple text payload. Keep review content
		// provider-neutral and let Slack render the markdown-style link syntax.
		value = map[string]string{"text": "*" + title + "*\n\n" + message}
	case domain.NotificationWebhook:
		value = event
	default:
		return nil, "", fmt.Errorf("unsupported notification provider %q", provider)
	}
	body, err := json.Marshal(value)
	if err != nil {
		return nil, "", fmt.Errorf("encode notification: %w", err)
	}
	return body, endpoint, nil
}

func notificationTitle(event domain.NotificationEvent) string {
	if event.Test {
		return "Open Review test notification"
	}
	switch event.State {
	case domain.RunCompleted:
		return "Open Review completed"
	case domain.RunNeedsAttention:
		return "Open Review needs attention"
	case domain.RunFailed:
		return "Open Review failed"
	default:
		return "Open Review status changed"
	}
}

func notificationMessage(event domain.NotificationEvent) string {
	if event.Test {
		return "This is a test notification from Open Review. No review data was evaluated or published. Check the delivery receipt in the Open Review console for the final provider result."
	}
	review := fmt.Sprintf("**%s #%d**", event.Repository, event.ReviewNumber)
	if event.ReviewURL != "" {
		review = fmt.Sprintf("[%s #%d](%s)", event.Repository, event.ReviewNumber, event.ReviewURL)
	}
	return fmt.Sprintf("%s · `%s`\n\nState: **%s** · Target: **%s** · Findings: **%d** · Highest severity: **%s**", review, shortSHA(event.HeadSHA), event.State, event.TargetBranch, event.FindingCount, event.HighestSeverity)
}

func shortSHA(value string) string {
	if len(value) > 12 {
		return value[:12]
	}
	return value
}

func hmacBase64(key, message []byte) string {
	mac := hmac.New(sha256.New, key)
	_, _ = mac.Write(message)
	return base64.StdEncoding.EncodeToString(mac.Sum(nil))
}

func hmacHex(key, message []byte) string {
	mac := hmac.New(sha256.New, key)
	_, _ = mac.Write(message)
	return hex.EncodeToString(mac.Sum(nil))
}

func validateWebhookURL(value string) error {
	parsed, err := url.Parse(strings.TrimSpace(value))
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.User != nil || parsed.Fragment != "" {
		return fmt.Errorf("webhook URL must be HTTPS without credentials or fragment")
	}
	return nil
}
