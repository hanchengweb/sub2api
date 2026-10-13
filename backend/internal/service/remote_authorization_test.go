package service

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

type remoteUsers struct{ user *User }

func (r *remoteUsers) GetByID(context.Context, int64) (*User, error) { return r.user, nil }

type remoteKeys struct{ key *APIKey }

func (r *remoteKeys) GetByID(context.Context, int64) (*APIKey, error) { return r.key, nil }

type remoteSessions struct {
	RefreshTokenCache
	active bool
}

func (r *remoteSessions) GetFamilyTokenHashes(context.Context, string) ([]string, error) {
	if r.active {
		return []string{"hash"}, nil
	}
	return nil, nil
}
func (r *remoteSessions) GetRefreshToken(context.Context, string) (*RefreshTokenData, error) {
	return &RefreshTokenData{UserID: 7, TokenVersion: 2, FamilyID: "session", ExpiresAt: time.Now().Add(time.Hour)}, nil
}

func TestRemoteAuthorizationBoundaries(t *testing.T) {
	ctx := context.Background()
	cache := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: cache.Addr()})
	defer rdb.Close()
	users := &remoteUsers{&User{ID: 7, Status: "active", TokenVersion: 2, Username: "Personal"}}
	keys := &remoteKeys{&APIKey{ID: 9, UserID: 7, Status: StatusAPIKeyActive}}
	sessions := &remoteSessions{active: true}
	svc := &RemoteAuthorizationService{rdb, users, keys, sessions}
	verifier := strings.Repeat("v", 43)
	hash := sha256.Sum256([]byte(verifier))
	challenge := base64.RawURLEncoding.EncodeToString(hash[:])
	req := RemoteAuthorizationRequest{RemoteClientID, RemoteCallback, strings.Repeat("s", 43), challenge, "S256"}
	bad := req
	bad.RedirectURI += "/evil"
	_, err := svc.Authorize(ctx, 7, "session", bad)
	require.Error(t, err)
	code, err := svc.Authorize(ctx, 7, "session", req)
	require.NoError(t, err)
	_, _, err = svc.Exchange(ctx, code, strings.Repeat("x", 43), req.State, "browser")
	require.Error(t, err)
	_, _, err = svc.Exchange(ctx, code, verifier, req.State, "desktop")
	require.Error(t, err)
	var wins atomic.Int32
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _, err := svc.Exchange(ctx, code, verifier, req.State, "browser")
			if err == nil {
				wins.Add(1)
			}
		}()
	}
	wg.Wait()
	require.EqualValues(t, 1, wins.Load())
	code, err = svc.Authorize(ctx, 7, "session", req)
	require.NoError(t, err)
	token, identity, err := svc.Exchange(ctx, code, verifier, req.State, "browser")
	require.NoError(t, err)
	require.Equal(t, RemoteAudience, identity.Audience)
	require.Equal(t, "7", identity.Subject)
	_, err = svc.Inspect(ctx, token)
	require.NoError(t, err)
	sessions.active = false
	_, err = svc.Inspect(ctx, token)
	require.Error(t, err)
	sessions.active = true
	require.NoError(t, svc.Revoke(ctx, token))
	_, err = svc.Inspect(ctx, token)
	require.Error(t, err)
	code, _, err = svc.DesktopProof(ctx, keys.key, challenge)
	require.NoError(t, err)
	token, identity, err = svc.Exchange(ctx, code, verifier, "", "desktop")
	require.NoError(t, err)
	require.Equal(t, "desktop", identity.Purpose)
	_, err = svc.Inspect(ctx, code)
	require.Error(t, err) // Proofs are not grants.
	keys.key.Status = StatusAPIKeyDisabled
	_, err = svc.Inspect(ctx, token)
	require.Error(t, err)
	keys.key.Status = StatusAPIKeyActive
	users.user.TokenVersion++
	_, err = svc.Inspect(ctx, token)
	require.Error(t, err)
	users.user.TokenVersion--
	cache.FastForward(6 * time.Minute)
	_, err = svc.Inspect(ctx, token)
	require.Error(t, err)
}
