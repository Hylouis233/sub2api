//go:build unit

package service

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestAccountReplenishmentAlertTriggersForSingleLowQuotaAccount(t *testing.T) {
	t.Parallel()

	var calls atomic.Int32
	var got feishuTextWebhookPayload
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		require.NoError(t, json.NewDecoder(r.Body).Decode(&got))
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	settings := newMockSettingRepo()
	require.NoError(t, settings.Set(context.Background(), SettingKeyAccountReplenishmentWebhookURL, server.URL))

	repo := &accountReplenishmentAccountRepoStub{active: []Account{
		openAICodexTestAccount(1, "alice", true, 91.5, 50),
		openAICodexTestAccount(2, "bob", false, 10, 10),
	}}
	svc := NewAccountReplenishmentAlertService(repo, settings, nil)
	svc.cooldown = 0

	sent, err := svc.evaluateOnce(context.Background())
	require.NoError(t, err)
	require.True(t, sent)
	require.Equal(t, int32(1), calls.Load())
	require.Equal(t, "text", got.MsgType)
	require.Contains(t, got.Content.Text, "Sub2API 补号告警")
	require.Contains(t, got.Content.Text, "alice")
	require.Contains(t, got.Content.Text, "5小时剩余: 8.50%")
	require.NotContains(t, got.Content.Text, "7天剩余")
}

func TestAccountReplenishmentAlertSkipsWhenNotExactlyOneAvailable(t *testing.T) {
	t.Parallel()

	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	settings := newMockSettingRepo()
	require.NoError(t, settings.Set(context.Background(), SettingKeyAccountReplenishmentWebhookURL, server.URL))

	repo := &accountReplenishmentAccountRepoStub{active: []Account{
		openAICodexTestAccount(1, "alice", true, 95, 50),
		openAICodexTestAccount(2, "bob", true, 95, 50),
	}}
	svc := NewAccountReplenishmentAlertService(repo, settings, nil)
	svc.cooldown = 0

	sent, err := svc.evaluateOnce(context.Background())
	require.NoError(t, err)
	require.False(t, sent)
	require.Equal(t, int32(0), calls.Load())
}

func TestAccountReplenishmentAlertSkipsWhenRemainingQuotaAtThreshold(t *testing.T) {
	t.Parallel()

	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	settings := newMockSettingRepo()
	require.NoError(t, settings.Set(context.Background(), SettingKeyAccountReplenishmentWebhookURL, server.URL))

	repo := &accountReplenishmentAccountRepoStub{active: []Account{
		openAICodexTestAccount(1, "alice", true, 90, 20),
	}}
	svc := NewAccountReplenishmentAlertService(repo, settings, nil)
	svc.cooldown = 0

	sent, err := svc.evaluateOnce(context.Background())
	require.NoError(t, err)
	require.False(t, sent)
	require.Equal(t, int32(0), calls.Load())
}

type accountReplenishmentAccountRepoStub struct {
	mockAccountRepoForPlatform
	active []Account
}

func (r *accountReplenishmentAccountRepoStub) ListActive(ctx context.Context) ([]Account, error) {
	return r.active, nil
}

func openAICodexTestAccount(id int64, name string, schedulable bool, used5h float64, used7d float64) Account {
	extra := map[string]any{}
	if used5h >= 0 {
		extra["codex_5h_used_percent"] = used5h
	}
	if used7d >= 0 {
		extra["codex_7d_used_percent"] = used7d
	}
	return Account{
		ID:          id,
		Name:        strings.TrimSpace(name),
		Platform:    PlatformOpenAI,
		Type:        AccountTypeOAuth,
		Status:      StatusActive,
		Schedulable: schedulable,
		Extra:       extra,
	}
}
