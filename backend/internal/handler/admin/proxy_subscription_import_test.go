package admin

import (
	"encoding/base64"
	"strings"
	"testing"
)

func TestParseProxySubscriptionSupportsMihomoHTTPAndSOCKS(t *testing.T) {
	content := `
proxies:
  - name: http-node
    type: http
    server: proxy.example.com
    port: 8080
    username: user
    password: pass
  - name: socks-node
    type: socks5
    server: socks.example.com
    port: 1080
  - name: ss-node
    type: ss
    server: ss.example.com
    port: 8388
`

	result, err := parseProxySubscription(content, "sub")
	if err != nil {
		t.Fatalf("parseProxySubscription error = %v", err)
	}
	if result.Total != 3 {
		t.Fatalf("total = %d, want 3", result.Total)
	}
	if len(result.Parsed) != 2 {
		t.Fatalf("parsed = %d, want 2", len(result.Parsed))
	}
	if result.Unsupported != 1 {
		t.Fatalf("unsupported = %d, want 1", result.Unsupported)
	}

	first := result.Parsed[0]
	if first.Name != "sub http-node" || first.Protocol != "http" || first.Host != "proxy.example.com" || first.Port != 8080 {
		t.Fatalf("first proxy = %+v", first)
	}
	if first.Username != "user" || first.Password != "pass" {
		t.Fatalf("auth = %q/%q, want user/pass", first.Username, first.Password)
	}

	second := result.Parsed[1]
	if second.Protocol != "socks5" || second.Host != "socks.example.com" || second.Port != 1080 {
		t.Fatalf("second proxy = %+v", second)
	}
}

func TestParseProxySubscriptionSupportsPlainProxyURLList(t *testing.T) {
	content := `
http://user:pass@proxy.example.com:8080#primary
socks5h://socks.example.com:1080
not a proxy
`

	result, err := parseProxySubscription(content, "")
	if err != nil {
		t.Fatalf("parseProxySubscription error = %v", err)
	}
	if result.Total != 3 {
		t.Fatalf("total = %d, want 3", result.Total)
	}
	if len(result.Parsed) != 2 {
		t.Fatalf("parsed = %d, want 2", len(result.Parsed))
	}
	if result.Invalid != 1 {
		t.Fatalf("invalid = %d, want 1", result.Invalid)
	}
	if result.Parsed[0].Name != "primary" {
		t.Fatalf("name = %q, want primary", result.Parsed[0].Name)
	}
	if result.Parsed[1].Protocol != "socks5h" {
		t.Fatalf("protocol = %q, want socks5h", result.Parsed[1].Protocol)
	}
}

func TestParseProxySubscriptionSupportsURLSafeUnpaddedBase64(t *testing.T) {
	content := base64.RawURLEncoding.EncodeToString([]byte("http://user:pass@proxy.example.com:8080#primary"))

	result, err := parseProxySubscription(content, "")
	if err != nil {
		t.Fatalf("parseProxySubscription error = %v", err)
	}
	if len(result.Parsed) != 1 {
		t.Fatalf("parsed = %d, want 1", len(result.Parsed))
	}
	if result.Parsed[0].Name != "primary" || result.Parsed[0].Protocol != "http" {
		t.Fatalf("proxy = %+v", result.Parsed[0])
	}
}

func TestParseProxySubscriptionRejectsTooDeepBase64(t *testing.T) {
	content := "http://proxy.example.com:8080#primary"
	for i := 0; i < proxySubscriptionMaxBase64Depth+1; i++ {
		content = base64.RawURLEncoding.EncodeToString([]byte(content))
	}

	_, err := parseProxySubscription(content, "")
	if err == nil || !strings.Contains(err.Error(), "base64 nesting is too deep") {
		t.Fatalf("error = %v, want nesting error", err)
	}
}

func TestParseProxySubscriptionRejectsOversizedContent(t *testing.T) {
	_, err := parseProxySubscription(strings.Repeat("x", proxySubscriptionMaxBytes+1), "")
	if err == nil || !strings.Contains(err.Error(), "too large") {
		t.Fatalf("error = %v, want too large", err)
	}
}

func TestSubscriptionFetchURLRejectsPrivateAddress(t *testing.T) {
	if _, err := validateSubscriptionFetchURL(t.Context(), "http://127.0.0.1/sub.yaml"); err == nil {
		t.Fatal("expected private address error")
	}
}
