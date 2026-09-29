// Package agentadapter defines the small authenticated boundary between Open
// Review's control-plane worker and an isolated coding-agent adapter.
package agentadapter

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"strconv"
	"strings"
	"time"
)

const (
	HeaderTimestamp = "X-Open-Review-Agent-Timestamp"
	HeaderSignature = "X-Open-Review-Agent-Signature"
)

func Sign(secret, timestamp string, body []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write([]byte(timestamp))
	_, _ = mac.Write([]byte("."))
	_, _ = mac.Write(body)
	return "sha256=" + hex.EncodeToString(mac.Sum(nil))
}

// Verify rejects missing/old/future timestamps before checking the MAC. The
// durable delivery ID is separately deduplicated by the control plane.
func Verify(secret, timestamp, signature string, body []byte, now time.Time, maxSkew time.Duration) bool {
	if len(secret) < 32 || maxSkew <= 0 {
		return false
	}
	seconds, err := strconv.ParseInt(strings.TrimSpace(timestamp), 10, 64)
	if err != nil || seconds <= 0 || now.Sub(time.Unix(seconds, 0)).Abs() > maxSkew {
		return false
	}
	expected := Sign(secret, strings.TrimSpace(timestamp), body)
	return hmac.Equal([]byte(expected), []byte(strings.TrimSpace(signature)))
}
