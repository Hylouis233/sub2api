//go:build unit

package service

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

func TestOpenAI429FastPath_MarksOAuthAccountCoolingDown(t *testing.T) {
	svc := &OpenAIGatewayService{}
	account := &Account{ID: 42, Platform: PlatformOpenAI, Type: AccountTypeOAuth}
	apiKeyAccount := &Account{ID: 43, Platform: PlatformOpenAI, Type: AccountTypeAPIKey}

	shouldDisable := svc.handleOpenAIAccountUpstreamError(context.Background(), account, http.StatusTooManyRequests, http.Header{}, nil)
	apiKeyShouldDisable := svc.handleOpenAIAccountUpstreamError(context.Background(), apiKeyAccount, http.StatusTooManyRequests, http.Header{}, nil)

	require.False(t, shouldDisable)
	require.False(t, apiKeyShouldDisable)
	require.True(t, svc.isOpenAIAccountRuntimeBlocked(account))
	require.False(t, svc.isOpenAIAccountRuntimeBlocked(apiKeyAccount))
}

func TestOpenAI401FastPath_RefreshesBeforePermanentMark(t *testing.T) {
	account := Account{
		ID:       142,
		Platform: PlatformOpenAI,
		Type:     AccountTypeOAuth,
		Status:   StatusActive,
		Credentials: map[string]any{
			"access_token":  "old-access-token",
			"refresh_token": "old-refresh-token",
			"expires_at":    time.Now().Add(time.Hour).UTC().Format(time.RFC3339),
		},
	}
	repo := &accountTestOpenAI401Repo{account: account}
	oauthClient := &accountTestOpenAIOAuthClient{}
	oauthService := NewOpenAIOAuthService(nil, oauthClient)
	provider := NewOpenAITokenProvider(repo, nil, oauthService)
	rateLimitService := NewRateLimitService(repo, nil, &config.Config{}, nil, nil)
	svc := &OpenAIGatewayService{
		accountRepo:           repo,
		openAITokenProvider:   provider,
		rateLimitService:      rateLimitService,
		cfg:                   &config.Config{},
		openaiAccountStats:    nil,
		openaiWSPool:          nil,
		openaiScheduler:       nil,
		openaiWSResolver:      nil,
		codexSnapshotThrottle: nil,
	}

	shouldDisable := svc.handleOpenAIAccountUpstreamError(
		context.Background(),
		&account,
		http.StatusUnauthorized,
		http.Header{},
		[]byte(`{"error":{"code":"token_invalidated","message":"old access token invalidated"}}`),
	)

	require.False(t, shouldDisable)
	require.Equal(t, 1, oauthClient.refreshCalls)
	require.Equal(t, 1, repo.updateCalls)
	require.Equal(t, 0, repo.setErrorCalls)
	require.Equal(t, "new-access-token", repo.account.GetOpenAIAccessToken())
}

func TestOpenAIRuntimeBlock_AppliesToOpenAIAPIKeyWhenRateLimitServiceStopsScheduling(t *testing.T) {
	svc := &OpenAIGatewayService{}
	account := &Account{ID: 44, Platform: PlatformOpenAI, Type: AccountTypeAPIKey}

	svc.BlockAccountScheduling(account, time.Time{}, "custom_error_code")

	require.True(t, svc.isOpenAIAccountRuntimeBlocked(account))
}

func TestOpenAIRuntimeBlock_DoesNotApplyToOtherPlatforms(t *testing.T) {
	svc := &OpenAIGatewayService{}
	account := &Account{ID: 45, Platform: PlatformGemini, Type: AccountTypeOAuth}

	svc.BlockAccountScheduling(account, time.Time{}, "custom_error_code")

	require.False(t, svc.isOpenAIAccountRuntimeBlocked(account))
}

func TestOpenAIRuntimeBlocker_IgnoresNonOpenAIFromRateLimitService(t *testing.T) {
	gateway := &OpenAIGatewayService{}
	repo := &rateLimitAccountRepoStub{}
	rateLimitService := NewRateLimitService(repo, nil, &config.Config{}, nil, nil)
	rateLimitService.SetAccountRuntimeBlocker(gateway)
	account := &Account{ID: 45, Platform: PlatformGemini, Type: AccountTypeOAuth}

	shouldDisable := rateLimitService.HandleUpstreamError(context.Background(), account, http.StatusForbidden, http.Header{}, []byte("forbidden"))

	require.True(t, shouldDisable)
	require.False(t, gateway.isOpenAIAccountRuntimeBlocked(account))
}

func TestOpenAIRuntimeBlock_DoesNotShortenExistingBlock(t *testing.T) {
	svc := &OpenAIGatewayService{}
	account := &Account{ID: 46, Platform: PlatformOpenAI, Type: AccountTypeOAuth}
	longUntil := time.Now().Add(10 * time.Minute)

	svc.BlockAccountScheduling(account, longUntil, "oauth_401")
	svc.BlockAccountScheduling(account, time.Time{}, "upstream_disable")

	value, ok := svc.openaiAccountRuntimeBlockUntil.Load(account.ID)
	require.True(t, ok)
	actualUntil, ok := value.(time.Time)
	require.True(t, ok)
	require.WithinDuration(t, longUntil, actualUntil, time.Second)
}

func TestOpenAIRuntimeBlock_ClearAccountSchedulingBlock(t *testing.T) {
	svc := &OpenAIGatewayService{}
	account := &Account{ID: 47, Platform: PlatformOpenAI, Type: AccountTypeOAuth}

	svc.BlockAccountScheduling(account, time.Now().Add(time.Minute), "429")
	require.True(t, svc.isOpenAIAccountRuntimeBlocked(account))

	svc.ClearAccountSchedulingBlock(account.ID)
	require.False(t, svc.isOpenAIAccountRuntimeBlocked(account))
}

func TestOpenAIRuntimeBlock_AppliesProxyRuntimeBlockWithoutAccountBlock(t *testing.T) {
	svc := &OpenAIGatewayService{}
	proxyID := int64(101)
	account := &Account{
		ID:       48,
		Platform: PlatformOpenAI,
		Type:     AccountTypeOAuth,
		ProxyID:  &proxyID,
		Proxy:    &Proxy{ID: proxyID, Protocol: "socks5", Host: "host.docker.internal", Port: 17802},
	}

	svc.blockOpenAIProxyRuntime("proxy_id:101", time.Now().Add(time.Minute))

	require.True(t, svc.isOpenAIAccountRuntimeBlocked(account))
}

func TestOpenAIRuntimeBlock_ClearsExpiredAccountBlockButKeepsProxyBlock(t *testing.T) {
	svc := &OpenAIGatewayService{}
	proxyID := int64(102)
	account := &Account{
		ID:       49,
		Platform: PlatformOpenAI,
		Type:     AccountTypeOAuth,
		ProxyID:  &proxyID,
		Proxy:    &Proxy{ID: proxyID, Protocol: "socks5", Host: "host.docker.internal", Port: 17804},
	}

	svc.openaiAccountRuntimeBlockUntil.Store(account.ID, time.Now().Add(-time.Minute))
	svc.blockOpenAIProxyRuntime("proxy_id:102", time.Now().Add(time.Minute))

	require.True(t, svc.isOpenAIAccountRuntimeBlocked(account))
	_, accountBlockStillStored := svc.openaiAccountRuntimeBlockUntil.Load(account.ID)
	require.False(t, accountBlockStillStored)
}

func TestOpenAIUpstreamNetworkFailuresBlockAccountAndProxyAfterTwoFailures(t *testing.T) {
	svc := &OpenAIGatewayService{}
	proxyID := int64(103)
	account := &Account{
		ID:       50,
		Platform: PlatformOpenAI,
		Type:     AccountTypeOAuth,
		ProxyID:  &proxyID,
		Proxy:    &Proxy{ID: proxyID, Protocol: "socks5", Host: "host.docker.internal", Port: 17815},
	}
	err := errors.New(`Get "https://api.openai.com/v1/models": EOF`)

	svc.recordOpenAIUpstreamRequestFailure(context.Background(), account, "socks5://host.docker.internal:17815", err)
	require.False(t, svc.isOpenAIAccountRuntimeBlocked(account))

	svc.recordOpenAIUpstreamRequestFailure(context.Background(), account, "socks5://host.docker.internal:17815", err)
	require.True(t, svc.isOpenAIAccountRuntimeBlocked(account))
}

func TestOpenAIUpstreamStatusFailuresBlockAccountAndProxyAfterTwoFailures(t *testing.T) {
	svc := &OpenAIGatewayService{}
	proxyID := int64(105)
	account := &Account{
		ID:       52,
		Platform: PlatformOpenAI,
		Type:     AccountTypeOAuth,
		ProxyID:  &proxyID,
		Proxy:    &Proxy{ID: proxyID, Protocol: "socks5", Host: "host.docker.internal", Port: 17816},
	}
	body := []byte(`{"error":{"message":"Service temporarily unavailable"}}`)

	svc.recordOpenAIUpstreamStatusFailure(context.Background(), account, "socks5://host.docker.internal:17816", http.StatusServiceUnavailable, "Service temporarily unavailable", body)
	require.False(t, svc.isOpenAIAccountRuntimeBlocked(account))
	require.False(t, svc.isOpenAIProxyRuntimeBlocked(account))

	svc.recordOpenAIUpstreamStatusFailure(context.Background(), account, "socks5://host.docker.internal:17816", http.StatusServiceUnavailable, "Service temporarily unavailable", body)
	require.True(t, svc.isOpenAIAccountRuntimeBlocked(account))
	require.True(t, svc.isOpenAIProxyRuntimeBlocked(account))
}

func TestOpenAIUpstreamStreamTimeoutsBlockAccountAndProxyAfterTwoFailures(t *testing.T) {
	svc := &OpenAIGatewayService{}
	proxyID := int64(106)
	account := &Account{
		ID:       53,
		Platform: PlatformOpenAI,
		Type:     AccountTypeOAuth,
		ProxyID:  &proxyID,
		Proxy:    &Proxy{ID: proxyID, Protocol: "socks5", Host: "host.docker.internal", Port: 17817},
	}

	svc.recordOpenAIUpstreamStreamTimeout(context.Background(), account, "socks5://host.docker.internal:17817", "gpt-5.5", 75*time.Second)
	require.False(t, svc.isOpenAIAccountRuntimeBlocked(account))
	require.False(t, svc.isOpenAIProxyRuntimeBlocked(account))

	svc.recordOpenAIUpstreamStreamTimeout(context.Background(), account, "socks5://host.docker.internal:17817", "gpt-5.5", 75*time.Second)
	require.True(t, svc.isOpenAIAccountRuntimeBlocked(account))
	require.True(t, svc.isOpenAIProxyRuntimeBlocked(account))
}

func TestOpenAIUpstreamNetworkSuccessClearsConsecutiveFailureCounters(t *testing.T) {
	svc := &OpenAIGatewayService{}
	proxyID := int64(104)
	account := &Account{
		ID:       51,
		Platform: PlatformOpenAI,
		Type:     AccountTypeOAuth,
		ProxyID:  &proxyID,
		Proxy:    &Proxy{ID: proxyID, Protocol: "socks5", Host: "host.docker.internal", Port: 17814},
	}
	err := errors.New(`Get "https://api.openai.com/v1/responses": EOF`)

	svc.recordOpenAIUpstreamRequestFailure(context.Background(), account, "", err)
	svc.recordOpenAIUpstreamRequestSuccess(account)
	svc.recordOpenAIUpstreamRequestFailure(context.Background(), account, "", err)

	require.False(t, svc.isOpenAIAccountRuntimeBlocked(account))
}

func TestShouldStopOpenAIOAuth429Failover_OnlyDuringStorm(t *testing.T) {
	svc := &OpenAIGatewayService{}
	account := &Account{ID: 42, Platform: PlatformOpenAI, Type: AccountTypeOAuth}
	apiKeyAccount := &Account{ID: 43, Platform: PlatformOpenAI, Type: AccountTypeAPIKey}

	require.False(t, svc.ShouldStopOpenAIOAuth429Failover(account, http.StatusTooManyRequests, 1))

	for i := 0; i < openAIOAuth429StormThreshold; i++ {
		svc.recordOpenAIOAuth429()
	}

	require.True(t, svc.ShouldStopOpenAIOAuth429Failover(account, http.StatusTooManyRequests, 1))
	require.False(t, svc.ShouldStopOpenAIOAuth429Failover(apiKeyAccount, http.StatusTooManyRequests, 1))
	require.False(t, svc.ShouldStopOpenAIOAuth429Failover(account, http.StatusInternalServerError, 1))
	require.False(t, svc.ShouldStopOpenAIOAuth429Failover(account, http.StatusTooManyRequests, 0))
}
