package service

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/openai"
	"github.com/stretchr/testify/require"
)

func TestAccountTestService_RunTestBackgroundWithOptionsUsesSelectedModelAndPrompt(t *testing.T) {
	account := Account{
		ID:          91,
		Name:        "openai-compatible",
		Platform:    PlatformOpenAI,
		Type:        AccountTypeAPIKey,
		Concurrency: 1,
		Status:      StatusActive,
		Credentials: map[string]any{
			"api_key":  "sk-test",
			"base_url": "https://api.example.test",
		},
		Extra: map[string]any{
			"openai_responses_mode": "force_chat_completions",
		},
	}
	upstream := &httpUpstreamRecorder{resp: &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
		Body: io.NopCloser(strings.NewReader(`data: {"choices":[{"delta":{"content":"pong"}}]}
data: [DONE]

`)),
	}}
	svc := &AccountTestService{
		accountRepo:  stubOpenAIAccountRepo{accounts: []Account{account}},
		httpUpstream: upstream,
		cfg: &config.Config{Security: config.SecurityConfig{
			URLAllowlist: config.URLAllowlistConfig{Enabled: false},
		}},
	}

	result, err := svc.RunTestBackgroundWithOptions(
		context.Background(),
		account.ID,
		"gpt-5.5",
		"custom health check",
		AccountTestModeDefault,
	)

	require.NoError(t, err)
	require.Equal(t, "success", result.Status)
	require.Equal(t, "pong", result.ResponseText)
	require.Len(t, upstream.bodies, 1)

	var payload struct {
		Model    string `json:"model"`
		Messages []struct {
			Role    string `json:"role"`
			Content string `json:"content"`
		} `json:"messages"`
		Stream bool `json:"stream"`
	}
	require.NoError(t, json.Unmarshal(upstream.bodies[0], &payload))
	require.Equal(t, "gpt-5.5", payload.Model)
	require.True(t, payload.Stream)
	require.Len(t, payload.Messages, 1)
	require.Equal(t, "user", payload.Messages[0].Role)
	require.Equal(t, "custom health check", payload.Messages[0].Content)
}

func TestAccountTestService_RunTestBackgroundParsesOpenAICompletedOutputText(t *testing.T) {
	account := Account{
		ID:          93,
		Name:        "openai-oauth",
		Platform:    PlatformOpenAI,
		Type:        AccountTypeOAuth,
		Concurrency: 1,
		Status:      StatusActive,
		Credentials: map[string]any{
			"access_token": "test-token",
		},
	}
	upstream := &httpUpstreamRecorder{resp: &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
		Body: io.NopCloser(strings.NewReader(`event: response.completed
data: {"response":{"id":"resp_1","model":"gpt-5.5","status":"completed","output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"pong from completed"}]}]}}

`)),
	}}
	svc := &AccountTestService{
		accountRepo:  stubOpenAIAccountRepo{accounts: []Account{account}},
		httpUpstream: upstream,
		cfg: &config.Config{Security: config.SecurityConfig{
			URLAllowlist: config.URLAllowlistConfig{Enabled: false},
		}},
	}

	result, err := svc.RunTestBackgroundWithOptions(
		context.Background(),
		account.ID,
		"gpt-5.5",
		"custom health check",
		AccountTestModeDefault,
	)

	require.NoError(t, err)
	require.Equal(t, "success", result.Status)
	require.Equal(t, "pong from completed", result.ResponseText)
}

type accountTestOpenAI401Repo struct {
	AccountRepository
	account       Account
	setErrorCalls int
	updateCalls   int
}

func (r *accountTestOpenAI401Repo) GetByID(_ context.Context, id int64) (*Account, error) {
	if r.account.ID != id {
		return nil, errors.New("account not found")
	}
	acc := r.account
	return &acc, nil
}

func (r *accountTestOpenAI401Repo) Update(_ context.Context, account *Account) error {
	r.updateCalls++
	r.account.Credentials = cloneCredentials(account.Credentials)
	return nil
}

func (r *accountTestOpenAI401Repo) SetError(_ context.Context, _ int64, _ string) error {
	r.setErrorCalls++
	return nil
}

type accountTestOpenAIOAuthClient struct {
	refreshCalls int
}

func (c *accountTestOpenAIOAuthClient) ExchangeCode(_ context.Context, _, _, _, _, _ string) (*openai.TokenResponse, error) {
	return nil, errors.New("not implemented")
}

func (c *accountTestOpenAIOAuthClient) RefreshToken(ctx context.Context, refreshToken, proxyURL string) (*openai.TokenResponse, error) {
	return c.RefreshTokenWithClientID(ctx, refreshToken, proxyURL, "")
}

func (c *accountTestOpenAIOAuthClient) RefreshTokenWithClientID(_ context.Context, _, _, _ string) (*openai.TokenResponse, error) {
	c.refreshCalls++
	return &openai.TokenResponse{
		AccessToken:  "new-access-token",
		RefreshToken: "new-refresh-token",
		ExpiresIn:    3600,
	}, nil
}

func TestAccountTestService_OpenAIOAuth401RefreshesAndRetriesBeforeSetError(t *testing.T) {
	account := Account{
		ID:          92,
		Name:        "openai-oauth",
		Platform:    PlatformOpenAI,
		Type:        AccountTypeOAuth,
		Concurrency: 1,
		Status:      StatusActive,
		Credentials: map[string]any{
			"access_token":       "old-access-token",
			"refresh_token":      "old-refresh-token",
			"chatgpt_account_id": "acct_123",
			"expires_at":         time.Now().Add(time.Hour).UTC().Format(time.RFC3339),
		},
	}
	repo := &accountTestOpenAI401Repo{account: account}
	oauthClient := &accountTestOpenAIOAuthClient{}
	oauthService := NewOpenAIOAuthService(nil, oauthClient)
	provider := NewOpenAITokenProvider(repo, nil, oauthService)
	upstream := &httpUpstreamRecorder{responses: []*http.Response{
		{
			StatusCode: http.StatusUnauthorized,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(strings.NewReader(`{"error":{"message":"token expired"}}`)),
		},
		{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
			Body: io.NopCloser(strings.NewReader(`data: {"type":"response.output_text.delta","delta":"pong"}
data: {"type":"response.completed"}

`)),
		},
	}}
	svc := &AccountTestService{
		accountRepo:         repo,
		openAITokenProvider: provider,
		httpUpstream:        upstream,
		cfg: &config.Config{Security: config.SecurityConfig{
			URLAllowlist: config.URLAllowlistConfig{Enabled: false},
		}},
	}

	result, err := svc.RunTestBackgroundWithOptions(
		context.Background(),
		account.ID,
		"gpt-5.5",
		"custom health check",
		AccountTestModeDefault,
	)

	require.NoError(t, err)
	require.Equal(t, "success", result.Status)
	require.Equal(t, "pong", result.ResponseText)
	require.Equal(t, 1, oauthClient.refreshCalls)
	require.Equal(t, 0, repo.setErrorCalls)
	require.Equal(t, 1, repo.updateCalls)
	require.Len(t, upstream.requests, 2)
	require.Equal(t, "Bearer old-access-token", upstream.requests[0].Header.Get("Authorization"))
	require.Equal(t, "Bearer new-access-token", upstream.requests[1].Header.Get("Authorization"))
}
