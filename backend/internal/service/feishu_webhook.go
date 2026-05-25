package service

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
)

const feishuWebhookTimeout = 5 * time.Second

var feishuWebhookHTTPClient = &http.Client{Timeout: feishuWebhookTimeout}

type feishuTextWebhookPayload struct {
	MsgType string                   `json:"msg_type"`
	Content feishuTextWebhookContent `json:"content"`
}

type feishuTextWebhookContent struct {
	Text string `json:"text"`
}

func sendFeishuTextWebhook(ctx context.Context, webhookURL, text string) error {
	webhookURL = strings.TrimSpace(webhookURL)
	if webhookURL == "" {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}

	payload := feishuTextWebhookPayload{
		MsgType: "text",
		Content: feishuTextWebhookContent{
			Text: strings.TrimSpace(text),
		},
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal feishu webhook payload: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, webhookURL, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("create feishu webhook request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := feishuWebhookHTTPClient.Do(req)
	if err != nil {
		return fmt.Errorf("send feishu webhook: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("feishu webhook returned status %d", resp.StatusCode)
	}
	return nil
}
