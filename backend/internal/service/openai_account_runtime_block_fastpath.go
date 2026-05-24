package service

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"
)

const (
	openAIAccountStateUpdateTimeout       = 5 * time.Second
	openAIOAuth429FallbackCooldown        = 5 * time.Second
	openAIStopSchedulingBridgeCooldown    = 2 * time.Minute
	openAIOAuth429StormWindow             = 10 * time.Second
	openAIOAuth429StormThreshold          = 20
	openAIOAuth429StormMaxAccountSwitches = 1
)

func openAIAccountStateContext(ctx context.Context) (context.Context, context.CancelFunc) {
	base := context.Background()
	if ctx != nil {
		base = context.WithoutCancel(ctx)
	}
	return context.WithTimeout(base, openAIAccountStateUpdateTimeout)
}

func isOpenAIOAuthAccount(account *Account) bool {
	return account != nil && account.Platform == PlatformOpenAI && account.Type == AccountTypeOAuth
}

func isOpenAIAccount(account *Account) bool {
	return account != nil && account.Platform == PlatformOpenAI
}

func (s *OpenAIGatewayService) handleOpenAIAccountUpstreamError(ctx context.Context, account *Account, statusCode int, headers http.Header, responseBody []byte) bool {
	stateCtx, cancel := openAIAccountStateContext(ctx)
	defer cancel()

	if statusCode == http.StatusUnauthorized {
		if handled, shouldDisable := s.handleOpenAIOAuth401Refresh(stateCtx, account, responseBody); handled {
			return shouldDisable
		}
	}
	if statusCode == http.StatusTooManyRequests {
		s.markOpenAIOAuth429RateLimited(stateCtx, account, headers, responseBody)
	}
	if s == nil || account == nil || s.rateLimitService == nil {
		return false
	}
	shouldDisable := s.rateLimitService.HandleUpstreamError(stateCtx, account, statusCode, headers, responseBody)
	if shouldDisable {
		s.BlockAccountScheduling(account, time.Time{}, "upstream_disable")
	}
	return shouldDisable
}

func (s *OpenAIGatewayService) handleOpenAIOAuth401Refresh(ctx context.Context, account *Account, responseBody []byte) (bool, bool) {
	if s == nil || !isOpenAIOAuthAccount(account) || s.openAITokenProvider == nil {
		return false, false
	}
	if strings.TrimSpace(account.GetOpenAIRefreshToken()) == "" {
		return false, false
	}

	refreshed, err := s.openAITokenProvider.RefreshAccessTokenNow(ctx, account)
	if err == nil {
		if refreshed != nil {
			account.Credentials = refreshed.Credentials
		}
		if s.rateLimitService != nil {
			if _, recoverErr := s.rateLimitService.RecoverAccountState(ctx, account.ID, AccountRecoveryOptions{}); recoverErr != nil {
				slog.Warn("openai_oauth_401_recover_state_after_refresh_failed", "account_id", account.ID, "error", recoverErr)
			}
		}
		s.ClearAccountSchedulingBlock(account.ID)
		slog.Info("openai_oauth_401_refresh_succeeded", "account_id", account.ID)
		return true, false
	}

	if isNonRetryableRefreshError(err) {
		slog.Warn("openai_oauth_401_refresh_confirmed_invalid", "account_id", account.ID, "error", err)
		return false, false
	}

	s.markOpenAIOAuth401RefreshPending(ctx, account, responseBody, err)
	return true, true
}

func (s *OpenAIGatewayService) markOpenAIOAuth401RefreshPending(ctx context.Context, account *Account, responseBody []byte, refreshErr error) {
	if s == nil || account == nil {
		return
	}
	if s.rateLimitService != nil && s.rateLimitService.tokenCacheInvalidator != nil {
		if err := s.rateLimitService.tokenCacheInvalidator.InvalidateToken(ctx, account); err != nil {
			slog.Warn("openai_oauth_401_pending_invalidate_cache_failed", "account_id", account.ID, "error", err)
		}
	}
	if account.Credentials == nil {
		account.Credentials = make(map[string]any)
	}
	account.Credentials["expires_at"] = time.Now().Format(time.RFC3339)
	if s.accountRepo != nil {
		if err := persistAccountCredentials(ctx, s.accountRepo, account, account.Credentials); err != nil {
			slog.Warn("openai_oauth_401_pending_force_refresh_update_failed", "account_id", account.ID, "error", err)
		}
	}

	cooldownMinutes := 10
	if s.cfg != nil && s.cfg.RateLimit.OAuth401CooldownMinutes > 0 {
		cooldownMinutes = s.cfg.RateLimit.OAuth401CooldownMinutes
	}
	until := time.Now().Add(time.Duration(cooldownMinutes) * time.Minute)

	upstreamMsg := strings.TrimSpace(extractUpstreamErrorMessage(responseBody))
	upstreamMsg = sanitizeUpstreamErrorMessage(upstreamMsg)
	if upstreamMsg != "" {
		upstreamMsg = truncateForLog([]byte(upstreamMsg), 512)
	}
	reason := fmt.Sprintf("OAuth 401 refresh pending: refresh attempt failed temporarily: %v", refreshErr)
	if upstreamMsg != "" {
		reason = fmt.Sprintf("OAuth 401 refresh pending: %s; refresh attempt failed temporarily: %v", upstreamMsg, refreshErr)
	}

	if s.rateLimitService != nil {
		s.rateLimitService.notifyAccountSchedulingBlocked(account, until, "oauth_401_refresh_pending")
	}
	s.BlockAccountScheduling(account, until, "oauth_401_refresh_pending")
	if s.accountRepo != nil {
		if err := s.accountRepo.SetTempUnschedulable(ctx, account.ID, until, reason); err != nil {
			slog.Warn("openai_oauth_401_pending_set_temp_unschedulable_failed", "account_id", account.ID, "error", err)
		}
	}
}

func (s *OpenAIGatewayService) markOpenAIOAuth429RateLimited(ctx context.Context, account *Account, headers http.Header, responseBody []byte) {
	if s == nil || !isOpenAIOAuthAccount(account) {
		return
	}
	s.recordOpenAIOAuth429()

	cooldownUntil := time.Now().Add(openAIOAuth429FallbackCooldown)
	if s.rateLimitService != nil {
		if resetAt := s.rateLimitService.calculateOpenAI429ResetTime(headers); resetAt != nil && resetAt.After(time.Now()) {
			cooldownUntil = *resetAt
		} else if resetUnix := parseOpenAIRateLimitResetTime(responseBody); resetUnix != nil {
			if resetAt := time.Unix(*resetUnix, 0); resetAt.After(time.Now()) {
				cooldownUntil = resetAt
			}
		} else if cooldown, ok := s.rateLimitService.get429FallbackCooldown(ctx, account); ok && cooldown > 0 {
			cooldownUntil = time.Now().Add(cooldown)
		}
	}
	s.BlockAccountScheduling(account, cooldownUntil, "429")
}

func (s *OpenAIGatewayService) BlockAccountScheduling(account *Account, until time.Time, reason string) {
	if s == nil || !isOpenAIAccount(account) {
		return
	}
	now := time.Now()
	blockUntil := until
	if blockUntil.IsZero() || !blockUntil.After(now) {
		blockUntil = now.Add(openAIStopSchedulingBridgeCooldown)
	}

	for {
		current, loaded := s.openaiAccountRuntimeBlockUntil.Load(account.ID)
		if !loaded {
			actual, stored := s.openaiAccountRuntimeBlockUntil.LoadOrStore(account.ID, blockUntil)
			if !stored {
				return
			}
			current = actual
		}

		currentUntil, ok := current.(time.Time)
		if !ok || currentUntil.IsZero() {
			if s.openaiAccountRuntimeBlockUntil.CompareAndSwap(account.ID, current, blockUntil) {
				return
			}
			continue
		}
		if currentUntil.After(blockUntil) {
			return
		}
		if s.openaiAccountRuntimeBlockUntil.CompareAndSwap(account.ID, current, blockUntil) {
			return
		}
	}
}

func (s *OpenAIGatewayService) ClearAccountSchedulingBlock(accountID int64) {
	if s == nil || accountID <= 0 {
		return
	}
	s.openaiAccountRuntimeBlockUntil.Delete(accountID)
}

func (s *OpenAIGatewayService) isOpenAIAccountRuntimeBlocked(account *Account) bool {
	if s == nil || !isOpenAIAccount(account) {
		return false
	}
	value, ok := s.openaiAccountRuntimeBlockUntil.Load(account.ID)
	if !ok {
		return s.isOpenAIProxyRuntimeBlocked(account)
	}
	cooldownUntil, ok := value.(time.Time)
	if !ok || cooldownUntil.IsZero() {
		s.openaiAccountRuntimeBlockUntil.Delete(account.ID)
		return s.isOpenAIProxyRuntimeBlocked(account)
	}
	if time.Now().Before(cooldownUntil) {
		return true
	}
	s.openaiAccountRuntimeBlockUntil.Delete(account.ID)
	return s.isOpenAIProxyRuntimeBlocked(account)
}

func (s *OpenAIGatewayService) recordOpenAIOAuth429() {
	if s == nil {
		return
	}
	now := time.Now()
	windowStart := s.openaiOAuth429WindowStartUnixNano.Load()
	if windowStart == 0 || now.Sub(time.Unix(0, windowStart)) >= openAIOAuth429StormWindow {
		if s.openaiOAuth429WindowStartUnixNano.CompareAndSwap(windowStart, now.UnixNano()) {
			s.openaiOAuth429WindowCount.Store(1)
			return
		}
	}
	s.openaiOAuth429WindowCount.Add(1)
}

func (s *OpenAIGatewayService) isOpenAIOAuth429Storm() bool {
	if s == nil {
		return false
	}
	windowStart := s.openaiOAuth429WindowStartUnixNano.Load()
	if windowStart == 0 || time.Since(time.Unix(0, windowStart)) >= openAIOAuth429StormWindow {
		return false
	}
	return s.openaiOAuth429WindowCount.Load() >= openAIOAuth429StormThreshold
}

func (s *OpenAIGatewayService) ShouldStopOpenAIOAuth429Failover(account *Account, statusCode int, failedSwitches int) bool {
	if statusCode != http.StatusTooManyRequests || failedSwitches < openAIOAuth429StormMaxAccountSwitches {
		return false
	}
	if !isOpenAIOAuthAccount(account) {
		return false
	}
	return s.isOpenAIOAuth429Storm()
}
