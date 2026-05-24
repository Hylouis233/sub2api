package service

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
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
