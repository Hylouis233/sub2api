package admin

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/openai"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

type accountHandlerOpenAIRefreshClient struct {
	refreshCalls int32
}

func (c *accountHandlerOpenAIRefreshClient) ExchangeCode(context.Context, string, string, string, string, string) (*openai.TokenResponse, error) {
	return nil, errors.New("not implemented")
}

func (c *accountHandlerOpenAIRefreshClient) RefreshToken(context.Context, string, string) (*openai.TokenResponse, error) {
	atomic.AddInt32(&c.refreshCalls, 1)
	return nil, errors.New("unexpected refresh")
}

func (c *accountHandlerOpenAIRefreshClient) RefreshTokenWithClientID(context.Context, string, string, string) (*openai.TokenResponse, error) {
	atomic.AddInt32(&c.refreshCalls, 1)
	return nil, errors.New("unexpected refresh")
}

func TestAccountHandlerRefreshSingleAccountOpenAINoRefreshTokenFails(t *testing.T) {
	client := &accountHandlerOpenAIRefreshClient{}
	handler := NewAccountHandler(
		newStubAdminService(),
		nil,
		service.NewOpenAIOAuthService(nil, client),
		nil,
		nil,
		nil,
		nil,
		nil,
		nil,
		nil,
		nil,
		nil,
		nil,
	)
	expiresAt := time.Now().Add(30 * time.Minute).UTC().Format(time.RFC3339)
	account := &service.Account{
		ID:       99,
		Platform: service.PlatformOpenAI,
		Type:     service.AccountTypeOAuth,
		Status:   service.StatusError,
		Credentials: map[string]any{
			"access_token": "revoked-access-token",
			"expires_at":   expiresAt,
		},
	}

	updated, warning, err := handler.refreshSingleAccount(context.Background(), account)

	require.Error(t, err)
	require.Nil(t, updated)
	require.Empty(t, warning)
	require.Contains(t, err.Error(), "refresh_token is required")
	require.Zero(t, atomic.LoadInt32(&client.refreshCalls), "refresh must not be attempted without a refresh_token")
}
