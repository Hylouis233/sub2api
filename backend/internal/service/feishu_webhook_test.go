//go:build unit

package service

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestSendFeishuTextWebhookSendsTextPayload(t *testing.T) {
	t.Parallel()

	var got feishuTextWebhookPayload
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, http.MethodPost, r.Method)
		require.Equal(t, "application/json", r.Header.Get("Content-Type"))
		require.NoError(t, json.NewDecoder(r.Body).Decode(&got))
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	err := sendFeishuTextWebhook(context.Background(), server.URL, "hello")
	require.NoError(t, err)
	require.Equal(t, "text", got.MsgType)
	require.Equal(t, "hello", got.Content.Text)
}
