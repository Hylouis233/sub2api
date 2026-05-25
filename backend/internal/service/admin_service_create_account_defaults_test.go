//go:build unit

package service

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

type createAccountRepoStub struct {
	accountRepoStub
	account      *Account
	created      *Account
	updated      *Account
	boundAccount int64
	boundGroups  []int64
}

func (s *createAccountRepoStub) Create(_ context.Context, account *Account) error {
	if account.ID == 0 {
		account.ID = 101
	}
	s.created = account
	s.account = account
	return nil
}

func (s *createAccountRepoStub) GetByID(_ context.Context, _ int64) (*Account, error) {
	if s.account == nil {
		return nil, ErrAccountNotFound
	}
	return s.account, nil
}

func (s *createAccountRepoStub) Update(_ context.Context, account *Account) error {
	s.updated = account
	s.account = account
	return nil
}

func (s *createAccountRepoStub) BindGroups(_ context.Context, accountID int64, groupIDs []int64) error {
	s.boundAccount = accountID
	s.boundGroups = append([]int64(nil), groupIDs...)
	if s.account != nil && s.account.ID == accountID {
		s.account.GroupIDs = append([]int64(nil), groupIDs...)
	}
	return nil
}

type createGroupRepoStub struct {
	groupRepoStubForAdmin
	groups []Group
}

func (s *createGroupRepoStub) ListActiveByPlatform(_ context.Context, platform string) ([]Group, error) {
	out := make([]Group, 0, len(s.groups))
	for _, group := range s.groups {
		if group.Platform == platform && group.Status == StatusActive {
			out = append(out, group)
		}
	}
	return out, nil
}

func (s *createGroupRepoStub) GetByID(_ context.Context, id int64) (*Group, error) {
	for i := range s.groups {
		if s.groups[i].ID == id {
			return &s.groups[i], nil
		}
	}
	return nil, ErrGroupNotFound
}

type createProxyRepoStub struct {
	proxyRepoStub
	proxies []ProxyWithAccountCount
}

func (s *createProxyRepoStub) ListActiveWithAccountCount(_ context.Context) ([]ProxyWithAccountCount, error) {
	return append([]ProxyWithAccountCount(nil), s.proxies...), nil
}

func TestCreateAccountAutoBindsLeastLoadedProxyAndDefaultGroup(t *testing.T) {
	accountRepo := &createAccountRepoStub{}
	svc := &adminServiceImpl{
		accountRepo: accountRepo,
		groupRepo: &createGroupRepoStub{groups: []Group{
			{ID: 1, Name: "default", Platform: PlatformOpenAI, Status: StatusActive},
			{ID: 2, Name: "openai-default", Platform: PlatformOpenAI, Status: StatusActive},
		}},
		proxyRepo: &createProxyRepoStub{proxies: []ProxyWithAccountCount{
			{Proxy: Proxy{ID: 30, Status: StatusActive}, AccountCount: 3},
			{Proxy: Proxy{ID: 20, Status: StatusActive}, AccountCount: 0},
			{Proxy: Proxy{ID: 10, Status: StatusActive}, AccountCount: 0},
		}},
	}

	account, err := svc.CreateAccount(context.Background(), &CreateAccountInput{
		Name:          "batch-openai",
		Platform:      PlatformOpenAI,
		Type:          AccountTypeOAuth,
		Credentials:   map[string]any{"access_token": "at"},
		AutoBindProxy: true,
	})

	require.NoError(t, err)
	require.NotNil(t, account)
	require.NotNil(t, accountRepo.created.ProxyID)
	require.Equal(t, int64(10), *accountRepo.created.ProxyID)
	require.Equal(t, int64(101), accountRepo.boundAccount)
	require.Equal(t, []int64{1}, accountRepo.boundGroups)
}

func TestCreateAccountFallsBackToPlatformDefaultGroup(t *testing.T) {
	accountRepo := &createAccountRepoStub{}
	svc := &adminServiceImpl{
		accountRepo: accountRepo,
		groupRepo: &createGroupRepoStub{groups: []Group{
			{ID: 2, Name: "openai-default", Platform: PlatformOpenAI, Status: StatusActive},
		}},
	}

	_, err := svc.CreateAccount(context.Background(), &CreateAccountInput{
		Name:        "batch-openai",
		Platform:    PlatformOpenAI,
		Type:        AccountTypeOAuth,
		Credentials: map[string]any{"access_token": "at"},
	})

	require.NoError(t, err)
	require.Equal(t, []int64{2}, accountRepo.boundGroups)
}

func TestUpdateAccountAutoBindsProxyAndKeepsExistingGroupsWithDefault(t *testing.T) {
	accountRepo := &createAccountRepoStub{
		account: &Account{
			ID:          201,
			Name:        "existing-openai",
			Platform:    PlatformOpenAI,
			Type:        AccountTypeOAuth,
			Status:      StatusActive,
			Credentials: map[string]any{"access_token": "old"},
			GroupIDs:    []int64{7},
		},
	}
	svc := &adminServiceImpl{
		accountRepo: accountRepo,
		groupRepo: &createGroupRepoStub{groups: []Group{
			{ID: 1, Name: "default", Platform: PlatformOpenAI, Status: StatusActive},
			{ID: 7, Name: "legacy", Platform: PlatformOpenAI, Status: StatusActive},
		}},
		proxyRepo: &createProxyRepoStub{proxies: []ProxyWithAccountCount{
			{Proxy: Proxy{ID: 32, Status: StatusActive}, AccountCount: 0},
		}},
	}

	updated, err := svc.UpdateAccount(context.Background(), 201, &UpdateAccountInput{
		Credentials:            map[string]any{"access_token": "new"},
		AutoBindProxy:          true,
		EnsureDefaultGroupBind: true,
	})

	require.NoError(t, err)
	require.NotNil(t, updated.ProxyID)
	require.Equal(t, int64(32), *updated.ProxyID)
	require.Equal(t, []int64{7, 1}, accountRepo.boundGroups)
}
