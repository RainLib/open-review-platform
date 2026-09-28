package agentadapter

import (
	"testing"
	"time"
)

func TestVerifyBindsTimestampAndBody(t *testing.T) {
	secret := "0123456789abcdef0123456789abcdef"
	now := time.Unix(1_700_000_000, 0)
	body := []byte(`{"attempt_id":"a"}`)
	timestamp := "1700000000"
	signature := Sign(secret, timestamp, body)
	if !Verify(secret, timestamp, signature, body, now, 5*time.Minute) {
		t.Fatal("expected valid signed adapter callback")
	}
	if Verify(secret, timestamp, signature, []byte(`{"attempt_id":"b"}`), now, 5*time.Minute) {
		t.Fatal("signature must bind the exact body")
	}
	if Verify(secret, "1699999000", signature, body, now, 5*time.Minute) {
		t.Fatal("expired callback must be rejected")
	}
}
