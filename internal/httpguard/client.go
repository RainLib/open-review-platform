package httpguard

import (
	"net/http"
	"time"
)

// NoRedirects returns a copy of the supplied client that never follows an
// HTTP redirect. Authorization headers and private request bodies must stay
// bound to the explicitly admitted endpoint. The caller's client is not
// modified, so it may be shared with unrelated requests.
func NoRedirects(client *http.Client, defaultTimeout time.Duration) *http.Client {
	if client == nil {
		client = &http.Client{}
	}
	copy := *client
	if copy.Timeout == 0 {
		copy.Timeout = defaultTimeout
	}
	copy.CheckRedirect = func(_ *http.Request, _ []*http.Request) error {
		return http.ErrUseLastResponse
	}
	return &copy
}
