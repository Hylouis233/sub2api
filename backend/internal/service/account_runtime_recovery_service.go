package service

import (
	"context"
	"log"
	"sync"
	"time"
)

const accountRuntimeRecoveryBatchLimit = 200

type AccountRuntimeBlockRepository interface {
	ListAccountsWithExpiredRuntimeBlocks(ctx context.Context, now time.Time, limit int) ([]int64, error)
}

type AccountRuntimeRecoveryService struct {
	accountRepo  AccountRuntimeBlockRepository
	rateLimitSvc *RateLimitService
	interval     time.Duration
	stopCh       chan struct{}
	stopOnce     sync.Once
	wg           sync.WaitGroup
}

func NewAccountRuntimeRecoveryService(accountRepo AccountRuntimeBlockRepository, rateLimitSvc *RateLimitService, interval time.Duration) *AccountRuntimeRecoveryService {
	return &AccountRuntimeRecoveryService{
		accountRepo:  accountRepo,
		rateLimitSvc: rateLimitSvc,
		interval:     interval,
		stopCh:       make(chan struct{}),
	}
}

func (s *AccountRuntimeRecoveryService) Start() {
	if s == nil || s.accountRepo == nil || s.rateLimitSvc == nil || s.interval <= 0 {
		return
	}
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		ticker := time.NewTicker(s.interval)
		defer ticker.Stop()

		s.runOnce()
		for {
			select {
			case <-ticker.C:
				s.runOnce()
			case <-s.stopCh:
				return
			}
		}
	}()
}

func (s *AccountRuntimeRecoveryService) Stop() {
	if s == nil || s.stopCh == nil {
		return
	}
	s.stopOnce.Do(func() {
		close(s.stopCh)
	})
	s.wg.Wait()
}

func (s *AccountRuntimeRecoveryService) runOnce() {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	now := time.Now().UTC()
	ids, err := s.accountRepo.ListAccountsWithExpiredRuntimeBlocks(ctx, now, accountRuntimeRecoveryBatchLimit)
	if err != nil {
		log.Printf("[AccountRuntimeRecovery] list expired runtime blocks failed: %v", err)
		return
	}
	if len(ids) == 0 {
		return
	}

	recovered := 0
	failed := 0
	for _, id := range ids {
		result, err := s.rateLimitSvc.RecoverExpiredRuntimeState(ctx, id, now)
		if err != nil {
			failed++
			log.Printf("[AccountRuntimeRecovery] recover account %d failed: %v", id, err)
			continue
		}
		if result != nil && result.ClearedRateLimit {
			recovered++
		}
	}
	if recovered > 0 || failed > 0 {
		log.Printf("[AccountRuntimeRecovery] processed expired runtime blocks: candidates=%d recovered=%d failed=%d", len(ids), recovered, failed)
	}
}
