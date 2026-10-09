package service

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestAPIKeyService_AuthSnapshotPreservesAccountType(t *testing.T) {
	for _, accountType := range []string{AccountTypeOrganizationService, AccountTypePersonal} {
		t.Run(accountType, func(t *testing.T) {
			svc := &APIKeyService{}
			user := &User{ID: 2, Status: StatusActive, AccountType: accountType}
			if accountType == AccountTypeOrganizationService {
				user.OrganizationID = "campus-test"
			}
			snapshot := svc.snapshotFromAPIKey(context.Background(), &APIKey{ID: 1, UserID: user.ID, Status: StatusActive, User: user})
			encoded, err := json.Marshal(&APIKeyAuthCacheEntry{Snapshot: snapshot})
			require.NoError(t, err)
			var cached APIKeyAuthCacheEntry
			require.NoError(t, json.Unmarshal(encoded, &cached))
			key, ok, err := svc.applyAuthCacheEntry("test-key", &cached)
			require.NoError(t, err)
			require.True(t, ok)
			require.Equal(t, accountType, key.User.AccountType)
			require.Equal(t, user.OrganizationID, key.User.OrganizationID)
		})
	}
}

func TestAPIKeyService_RejectsV20AuthSnapshotWithoutAccountType(t *testing.T) {
	svc := &APIKeyService{}
	key, ok, err := svc.applyAuthCacheEntry("test-key", &APIKeyAuthCacheEntry{
		Snapshot: &APIKeyAuthSnapshot{Version: 20},
	})
	require.NoError(t, err)
	require.False(t, ok)
	require.Nil(t, key)
}

func TestAPIKeyService_RejectsV10AuthSnapshotWithoutModelsListConfig(t *testing.T) {
	groupID := int64(9)
	svc := &APIKeyService{}

	apiKey, ok, err := svc.applyAuthCacheEntry("k-legacy-models-list", &APIKeyAuthCacheEntry{
		Snapshot: &APIKeyAuthSnapshot{
			Version:  10,
			APIKeyID: 1,
			UserID:   2,
			GroupID:  &groupID,
			Status:   StatusActive,
			User: APIKeyAuthUserSnapshot{
				ID:          2,
				Status:      StatusActive,
				Role:        RoleUser,
				Balance:     10,
				Concurrency: 3,
			},
			Group: &APIKeyAuthGroupSnapshot{
				ID:               groupID,
				Name:             "openai",
				Platform:         PlatformOpenAI,
				Status:           StatusActive,
				SubscriptionType: SubscriptionTypeStandard,
				RateMultiplier:   1,
			},
		},
	})

	if err != nil {
		t.Fatalf("expected stale snapshot to be ignored without error, got %v", err)
	}
	if ok {
		t.Fatalf("expected v10 auth snapshot to be rejected after models_list_config was added")
	}
	if apiKey != nil {
		t.Fatalf("expected no API key from stale snapshot, got %#v", apiKey)
	}
}

func TestAPIKeyService_RejectsV15AuthSnapshotWithoutReasoningEffortPolicy(t *testing.T) {
	svc := &APIKeyService{}

	apiKey, ok, err := svc.applyAuthCacheEntry("k-legacy-reasoning-mappings", &APIKeyAuthCacheEntry{
		Snapshot: &APIKeyAuthSnapshot{Version: 15},
	})

	if err != nil {
		t.Fatalf("expected stale snapshot to be ignored without error, got %v", err)
	}
	if ok {
		t.Fatal("expected v15 auth snapshot to be rejected after reasoning effort policy was added")
	}
	if apiKey != nil {
		t.Fatalf("expected no API key from stale snapshot, got %#v", apiKey)
	}
}
