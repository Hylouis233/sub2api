package service

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

const openAIUpstreamNetworkFailureBlockCooldown = openAIStopSchedulingBridgeCooldown

type openAIConsecutiveFailureCounter struct {
	mu    sync.Mutex
	count int
}

func (c *openAIConsecutiveFailureCounter) add() int {
	if c == nil {
		return 0
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.count++
	return c.count
}

func (c *openAIConsecutiveFailureCounter) reset() {
	if c == nil {
		return
	}
	c.mu.Lock()
	c.count = 0
	c.mu.Unlock()
}

func (c *openAIConsecutiveFailureCounter) value() int {
	if c == nil {
		return 0
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.count
}

func (s *OpenAIGatewayService) openAIUpstreamAccountFailureKey(accountID int64) string {
	if accountID <= 0 {
		return ""
	}
	return fmt.Sprintf("account:%d", accountID)
}

func (s *OpenAIGatewayService) openAIUpstreamProxyFailureKey(account *Account, proxyURL string) string {
	if account != nil && account.ProxyID != nil && *account.ProxyID > 0 {
		return fmt.Sprintf("proxy_id:%d", *account.ProxyID)
	}
	if account != nil && account.Proxy != nil {
		if account.Proxy.ID > 0 {
			return fmt.Sprintf("proxy_id:%d", account.Proxy.ID)
		}
		if url := strings.TrimSpace(account.Proxy.URL()); url != "" {
			return "proxy_url:" + url
		}
	}
	if proxyURL = strings.TrimSpace(proxyURL); proxyURL != "" {
		return "proxy_url:" + proxyURL
	}
	return ""
}

func (s *OpenAIGatewayService) recordOpenAIUpstreamRequestSuccess(account *Account) {
	if s == nil || account == nil {
		return
	}
	s.clearOpenAIUpstreamFailureCounter(&s.openaiAccountNetworkFailureCounts, s.openAIUpstreamAccountFailureKey(account.ID))
	s.clearOpenAIProxyRuntimeBlock(s.openAIUpstreamProxyFailureKey(account, ""))
	s.clearOpenAIUpstreamFailureCounter(&s.openaiProxyNetworkFailureCounts, s.openAIUpstreamProxyFailureKey(account, ""))
}

func (s *OpenAIGatewayService) recordOpenAIUpstreamRequestFailure(ctx context.Context, account *Account, proxyURL string, err error) {
	if s == nil || account == nil || err == nil {
		return
	}
	now := time.Now()
	accountKey := s.openAIUpstreamAccountFailureKey(account.ID)
	if accountKey != "" {
		if count := s.noteOpenAIUpstreamFailure(&s.openaiAccountNetworkFailureCounts, accountKey); count >= 2 {
			s.BlockAccountScheduling(account, now.Add(openAIUpstreamNetworkFailureBlockCooldown), "upstream_network_failures")
		}
	}
	proxyKey := s.openAIUpstreamProxyFailureKey(account, proxyURL)
	if proxyKey != "" {
		if count := s.noteOpenAIUpstreamFailure(&s.openaiProxyNetworkFailureCounts, proxyKey); count >= 2 {
			s.blockOpenAIProxyRuntime(proxyKey, now.Add(openAIUpstreamNetworkFailureBlockCooldown))
		}
	}
	safeErr := sanitizeUpstreamErrorMessage(err.Error())
	zap.L().Warn("openai upstream request failed",
		zap.Int64("account_id", account.ID),
		zap.String("proxy_key", proxyKey),
		zap.String("error", safeErr),
	)
}

func (s *OpenAIGatewayService) openAIUpstreamRequestErrorFailover(ctx context.Context, c *gin.Context, account *Account, proxyURL string, err error, passthrough bool) *UpstreamFailoverError {
	if s == nil || account == nil || err == nil {
		return &UpstreamFailoverError{
			StatusCode:   http.StatusBadGateway,
			ResponseBody: []byte(`{"error":{"type":"upstream_error","message":"Upstream request failed"}}`),
		}
	}
	safeErr := sanitizeUpstreamErrorMessage(err.Error())
	setOpsUpstreamError(c, 0, safeErr, "")
	appendOpsUpstreamError(c, OpsUpstreamErrorEvent{
		Platform:           account.Platform,
		AccountID:          account.ID,
		AccountName:        account.Name,
		UpstreamStatusCode: 0,
		Passthrough:        passthrough,
		Kind:               "request_error",
		Message:            safeErr,
	})
	s.recordOpenAIUpstreamRequestFailure(ctx, account, proxyURL, err)
	body, marshalErr := json.Marshal(gin.H{
		"error": gin.H{
			"type":    "upstream_error",
			"message": "Upstream request failed",
		},
	})
	if marshalErr != nil {
		body = []byte(`{"error":{"type":"upstream_error","message":"Upstream request failed"}}`)
	}
	return &UpstreamFailoverError{
		StatusCode:             http.StatusBadGateway,
		ResponseBody:           body,
		RetryableOnSameAccount: false,
	}
}

func (s *OpenAIGatewayService) noteOpenAIUpstreamFailure(counterMap *sync.Map, key string) int {
	if s == nil || counterMap == nil || key == "" {
		return 0
	}
	actual, _ := counterMap.LoadOrStore(key, &openAIConsecutiveFailureCounter{})
	counter, _ := actual.(*openAIConsecutiveFailureCounter)
	if counter == nil {
		counter = &openAIConsecutiveFailureCounter{}
		actual, _ = counterMap.LoadOrStore(key, counter)
		counter, _ = actual.(*openAIConsecutiveFailureCounter)
	}
	if counter == nil {
		return 0
	}
	return counter.add()
}

func (s *OpenAIGatewayService) clearOpenAIUpstreamFailureCounter(counterMap *sync.Map, key string) {
	if s == nil || counterMap == nil || key == "" {
		return
	}
	if value, ok := counterMap.LoadAndDelete(key); ok {
		if counter, _ := value.(*openAIConsecutiveFailureCounter); counter != nil {
			counter.reset()
		}
	}
}

func (s *OpenAIGatewayService) blockOpenAIProxyRuntime(proxyKey string, until time.Time) {
	if s == nil || proxyKey == "" {
		return
	}
	now := time.Now()
	blockUntil := until
	if blockUntil.IsZero() || !blockUntil.After(now) {
		blockUntil = now.Add(openAIUpstreamNetworkFailureBlockCooldown)
	}
	for {
		current, loaded := s.openaiProxyRuntimeBlockUntil.Load(proxyKey)
		if !loaded {
			actual, stored := s.openaiProxyRuntimeBlockUntil.LoadOrStore(proxyKey, blockUntil)
			if !stored {
				return
			}
			current = actual
		}
		currentUntil, ok := current.(time.Time)
		if !ok || currentUntil.IsZero() {
			if s.openaiProxyRuntimeBlockUntil.CompareAndSwap(proxyKey, current, blockUntil) {
				return
			}
			continue
		}
		if currentUntil.After(blockUntil) {
			return
		}
		if s.openaiProxyRuntimeBlockUntil.CompareAndSwap(proxyKey, current, blockUntil) {
			return
		}
	}
}

func (s *OpenAIGatewayService) clearOpenAIProxyRuntimeBlock(proxyKey string) {
	if s == nil || proxyKey == "" {
		return
	}
	s.openaiProxyRuntimeBlockUntil.Delete(proxyKey)
}

func (s *OpenAIGatewayService) isOpenAIProxyRuntimeBlocked(account *Account) bool {
	if s == nil || account == nil {
		return false
	}
	proxyKey := s.openAIUpstreamProxyFailureKey(account, "")
	if proxyKey == "" {
		return false
	}
	value, ok := s.openaiProxyRuntimeBlockUntil.Load(proxyKey)
	if !ok {
		return false
	}
	cooldownUntil, ok := value.(time.Time)
	if !ok || cooldownUntil.IsZero() {
		s.openaiProxyRuntimeBlockUntil.Delete(proxyKey)
		return false
	}
	if time.Now().Before(cooldownUntil) {
		return true
	}
	s.openaiProxyRuntimeBlockUntil.Delete(proxyKey)
	return false
}
