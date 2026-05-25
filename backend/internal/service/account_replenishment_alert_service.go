package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"
)

const (
	accountReplenishmentAlertInterval = time.Minute
	accountReplenishmentAlertTimeout  = 20 * time.Second
	accountReplenishmentAlertCooldown = time.Hour
	accountReplenishmentAlertRedisKey = "alerts:account_replenishment:single_openai_codex_low_quota"
)

type AccountReplenishmentAlertService struct {
	accountRepo AccountRepository
	settingRepo SettingRepository
	redisClient *redis.Client

	interval time.Duration
	cooldown time.Duration

	stopCh    chan struct{}
	startOnce sync.Once
	stopOnce  sync.Once
	wg        sync.WaitGroup

	mu         sync.Mutex
	lastSentAt time.Time
}

type accountQuotaWindowSnapshot struct {
	Label       string
	UsedPercent float64
	Remaining   float64
	ResetsAt    *time.Time
}

func NewAccountReplenishmentAlertService(accountRepo AccountRepository, settingRepo SettingRepository, redisClient *redis.Client) *AccountReplenishmentAlertService {
	return &AccountReplenishmentAlertService{
		accountRepo: accountRepo,
		settingRepo: settingRepo,
		redisClient: redisClient,
		interval:    accountReplenishmentAlertInterval,
		cooldown:    accountReplenishmentAlertCooldown,
	}
}

func (s *AccountReplenishmentAlertService) Start() {
	if s == nil {
		return
	}
	s.startOnce.Do(func() {
		if s.stopCh == nil {
			s.stopCh = make(chan struct{})
		}
		s.wg.Add(1)
		go s.run()
	})
}

func (s *AccountReplenishmentAlertService) Stop() {
	if s == nil {
		return
	}
	s.stopOnce.Do(func() {
		if s.stopCh != nil {
			close(s.stopCh)
		}
	})
	s.wg.Wait()
}

func (s *AccountReplenishmentAlertService) run() {
	defer s.wg.Done()

	timer := time.NewTimer(0)
	defer timer.Stop()

	for {
		select {
		case <-timer.C:
			ctx, cancel := context.WithTimeout(context.Background(), accountReplenishmentAlertTimeout)
			_, _ = s.evaluateOnce(ctx)
			cancel()
			timer.Reset(s.effectiveInterval())
		case <-s.stopCh:
			return
		}
	}
}

func (s *AccountReplenishmentAlertService) effectiveInterval() time.Duration {
	if s == nil || s.interval <= 0 {
		return accountReplenishmentAlertInterval
	}
	return s.interval
}

func (s *AccountReplenishmentAlertService) evaluateOnce(ctx context.Context) (bool, error) {
	if s == nil || s.accountRepo == nil || s.settingRepo == nil {
		return false, nil
	}
	if ctx == nil {
		ctx = context.Background()
	}

	webhookURL, err := s.settingRepo.GetValue(ctx, SettingKeyAccountReplenishmentWebhookURL)
	if err != nil {
		if errors.Is(err, ErrSettingNotFound) {
			return false, nil
		}
		return false, fmt.Errorf("load account replenishment webhook setting: %w", err)
	}
	webhookURL = strings.TrimSpace(webhookURL)
	if webhookURL == "" {
		return false, nil
	}

	accounts, err := s.accountRepo.ListActive(ctx)
	if err != nil {
		return false, fmt.Errorf("list active accounts for replenishment alert: %w", err)
	}

	now := time.Now().UTC()
	var candidates []Account
	var available []Account
	for _, account := range accounts {
		if !isOpenAICodexQuotaAccount(account) {
			continue
		}
		candidates = append(candidates, account)
		if account.IsSchedulable() {
			available = append(available, account)
		}
	}
	if len(available) != 1 {
		return false, nil
	}

	quotaWindows := collectCodexQuotaWindows(available[0], now)
	lowWindows := filterLowRemainingQuotaWindows(quotaWindows, 10)
	if len(lowWindows) == 0 {
		return false, nil
	}
	if !s.allowSend(ctx) {
		return false, nil
	}

	text := buildAccountReplenishmentAlertText(available[0], len(candidates), lowWindows, now)
	if err := sendFeishuTextWebhook(ctx, webhookURL, text); err != nil {
		s.releaseCooldown(ctx)
		slog.Warn("account_replenishment_alert: send feishu webhook failed", "error", err)
		return false, err
	}
	s.markSent(now)
	return true, nil
}

func isOpenAICodexQuotaAccount(account Account) bool {
	if strings.TrimSpace(account.Platform) != PlatformOpenAI {
		return false
	}
	switch strings.TrimSpace(account.Type) {
	case AccountTypeOAuth, AccountTypeSetupToken:
		return true
	default:
		return false
	}
}

func collectCodexQuotaWindows(account Account, now time.Time) []accountQuotaWindowSnapshot {
	windows := []accountQuotaWindowSnapshot{}
	for _, item := range []struct {
		window string
		label  string
	}{
		{window: "5h", label: "5小时"},
		{window: "7d", label: "7天"},
	} {
		progress := buildCodexUsageProgressFromExtra(account.Extra, item.window, now)
		if progress == nil {
			continue
		}
		remaining := clampPercent(100 - progress.Utilization)
		windows = append(windows, accountQuotaWindowSnapshot{
			Label:       item.label,
			UsedPercent: progress.Utilization,
			Remaining:   remaining,
			ResetsAt:    progress.ResetsAt,
		})
	}
	return windows
}

func filterLowRemainingQuotaWindows(windows []accountQuotaWindowSnapshot, threshold float64) []accountQuotaWindowSnapshot {
	low := []accountQuotaWindowSnapshot{}
	for _, window := range windows {
		if window.Remaining < threshold {
			low = append(low, window)
		}
	}
	return low
}

func clampPercent(v float64) float64 {
	if v < 0 {
		return 0
	}
	if v > 100 {
		return 100
	}
	return v
}

func (s *AccountReplenishmentAlertService) allowSend(ctx context.Context) bool {
	cooldown := s.cooldown
	if cooldown <= 0 {
		return true
	}
	if s.redisClient != nil {
		ok, err := s.redisClient.SetNX(ctx, accountReplenishmentAlertRedisKey, time.Now().UTC().Format(time.RFC3339Nano), cooldown).Result()
		if err == nil {
			return ok
		}
		slog.Warn("account_replenishment_alert: redis cooldown failed, falling back to process memory", "error", err)
	}

	now := time.Now().UTC()
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.lastSentAt.IsZero() && now.Sub(s.lastSentAt) < cooldown {
		return false
	}
	s.lastSentAt = now
	return true
}

func (s *AccountReplenishmentAlertService) releaseCooldown(ctx context.Context) {
	if s == nil {
		return
	}
	if s.redisClient != nil {
		_ = s.redisClient.Del(ctx, accountReplenishmentAlertRedisKey).Err()
		return
	}
	s.mu.Lock()
	s.lastSentAt = time.Time{}
	s.mu.Unlock()
}

func (s *AccountReplenishmentAlertService) markSent(now time.Time) {
	if s == nil || s.redisClient != nil {
		return
	}
	s.mu.Lock()
	s.lastSentAt = now
	s.mu.Unlock()
}

func buildAccountReplenishmentAlertText(account Account, totalCandidates int, lowWindows []accountQuotaWindowSnapshot, now time.Time) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Sub2API 补号告警\n")
	fmt.Fprintf(&b, "当前仅 1 个 OpenAI Codex 账号可调度，且该账号剩余额度低于 10%%。\n")
	fmt.Fprintf(&b, "可调度账号: %s (ID: %d)\n", fallbackAccountName(account), account.ID)
	fmt.Fprintf(&b, "OpenAI Codex 活跃账号数: %d\n", totalCandidates)
	for _, window := range lowWindows {
		fmt.Fprintf(&b, "%s剩余: %.2f%% (已用 %.2f%%)", window.Label, window.Remaining, window.UsedPercent)
		if window.ResetsAt != nil {
			fmt.Fprintf(&b, "，重置时间: %s", window.ResetsAt.UTC().Format(time.RFC3339))
		}
		b.WriteString("\n")
	}
	fmt.Fprintf(&b, "触发时间: %s", now.UTC().Format(time.RFC3339))
	return b.String()
}

func fallbackAccountName(account Account) string {
	if strings.TrimSpace(account.Name) != "" {
		return strings.TrimSpace(account.Name)
	}
	return "-"
}

// ProvideAccountReplenishmentAlertService creates and starts AccountReplenishmentAlertService.
func ProvideAccountReplenishmentAlertService(
	accountRepo AccountRepository,
	settingRepo SettingRepository,
	redisClient *redis.Client,
) *AccountReplenishmentAlertService {
	svc := NewAccountReplenishmentAlertService(accountRepo, settingRepo, redisClient)
	svc.Start()
	return svc
}
