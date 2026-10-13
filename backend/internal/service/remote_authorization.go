package service

// Remote authorization is an opaque, purpose-limited grant. It cannot be used
// as a WindHub login, model key, refresh token or tenant-platform credential.
import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"regexp"
	"strconv"
	"time"

	"github.com/redis/go-redis/v9"
)

const RemoteClientID = "wxm-mobile-remote"
const RemoteCallback = "https://app.wemoreai.com/remote/v1/personal/callback"
const RemoteIssuer = "https://windhub.online"
const RemoteAudience = "https://app.wemoreai.com/remote/v1"

var remoteChallenge = regexp.MustCompile(`^[A-Za-z0-9_-]{43}$`)
var remoteVerifier = regexp.MustCompile(`^[A-Za-z0-9._~-]{43,128}$`)

type RemoteAuthorizationRequest struct {
	ClientID    string `json:"client_id"`
	RedirectURI string `json:"redirect_uri"`
	State       string `json:"state"`
	Challenge   string `json:"code_challenge"`
	Method      string `json:"code_challenge_method"`
}

func (r RemoteAuthorizationRequest) Valid() bool {
	return r.ClientID == RemoteClientID && r.RedirectURI == RemoteCallback && remoteChallenge.MatchString(r.State) && remoteChallenge.MatchString(r.Challenge) && r.Method == "S256"
}

type remoteGrant struct {
	UserID    int64  `json:"user_id"`
	Version   int64  `json:"version"`
	SessionID string `json:"session_id,omitempty"`
	KeyID     int64  `json:"key_id,omitempty"`
	Purpose   string `json:"purpose"`
	State     string `json:"state,omitempty"`
	Challenge string `json:"challenge,omitempty"`
	Expires   int64  `json:"expires"`
}

type RemoteIdentity struct {
	Issuer          string `json:"issuer"`
	Subject         string `json:"subject"`
	Audience        string `json:"audience"`
	Purpose         string `json:"purpose"`
	Name            string `json:"name"`
	AuthorizationID string `json:"authorization_id"`
	ExpiresAt       int64  `json:"expires_at"`
}

type remoteUserReader interface {
	GetByID(context.Context, int64) (*User, error)
}
type remoteKeyReader interface {
	GetByID(context.Context, int64) (*APIKey, error)
}
type RemoteAuthorizationService struct {
	cache    redis.UniversalClient
	users    remoteUserReader
	keys     remoteKeyReader
	sessions RefreshTokenCache
}

func NewRemoteAuthorizationService(cache redis.UniversalClient, users remoteUserReader, keys remoteKeyReader, auth *AuthService) *RemoteAuthorizationService {
	return &RemoteAuthorizationService{cache: cache, users: users, keys: keys, sessions: auth.refreshTokenCache}
}

func remoteCacheKey(kind, token string) string {
	hash := sha256.Sum256([]byte(token))
	return "wxm_remote:" + kind + ":" + hex.EncodeToString(hash[:])
}

func (s *RemoteAuthorizationService) put(ctx context.Context, kind string, grant remoteGrant, ttl time.Duration) (string, error) {
	bytes := make([]byte, 32)
	if _, err := rand.Read(bytes); err != nil {
		return "", err
	}
	token := "wr_" + base64.RawURLEncoding.EncodeToString(bytes)
	grant.Expires = time.Now().Add(ttl).Unix()
	raw, err := json.Marshal(grant)
	if err != nil {
		return "", err
	}
	if err = s.cache.Set(ctx, remoteCacheKey(kind, token), raw, ttl).Err(); err != nil {
		return "", ErrServiceUnavailable
	}
	return token, nil
}

func (s *RemoteAuthorizationService) get(ctx context.Context, kind, token string) (remoteGrant, string, error) {
	var grant remoteGrant
	if len(token) != 46 || token[:3] != "wr_" {
		return grant, "", ErrInvalidToken
	}
	raw, err := s.cache.Get(ctx, remoteCacheKey(kind, token)).Result()
	if err == redis.Nil {
		return grant, "", ErrInvalidToken
	}
	if err != nil {
		return grant, "", ErrServiceUnavailable
	}
	if json.Unmarshal([]byte(raw), &grant) != nil || grant.Expires <= time.Now().Unix() {
		return grant, "", ErrInvalidToken
	}
	return grant, raw, nil
}

func (s *RemoteAuthorizationService) identity(ctx context.Context, g remoteGrant) (*RemoteIdentity, error) {
	user, err := s.users.GetByID(ctx, g.UserID)
	if err != nil || user == nil || !user.IsActive() || !user.CanLogin() || user.TokenVersion != g.Version {
		return nil, ErrTokenRevoked
	}
	authorizationID := g.SessionID
	if g.Purpose == "desktop" {
		key, err := s.keys.GetByID(ctx, g.KeyID)
		if err != nil || key == nil || key.UserID != g.UserID || !key.IsActive() || (key.ExpiresAt != nil && !key.ExpiresAt.After(time.Now())) {
			return nil, ErrTokenRevoked
		}
		authorizationID = "key:" + strconv.FormatInt(key.ID, 10) + ":" + strconv.FormatInt(user.TokenVersion, 10)
	} else if g.Purpose == "browser" {
		if g.SessionID == "" || s.sessions == nil {
			return nil, ErrTokenRevoked
		}
		hashes, err := s.sessions.GetFamilyTokenHashes(ctx, g.SessionID)
		if err != nil {
			return nil, ErrServiceUnavailable
		}
		active := false
		for _, hash := range hashes {
			token, err := s.sessions.GetRefreshToken(ctx, hash)
			if err == nil && token.UserID == g.UserID && token.TokenVersion == g.Version && token.FamilyID == g.SessionID && token.ExpiresAt.After(time.Now()) {
				active = true
				break
			}
		}
		if !active {
			return nil, ErrTokenRevoked
		}
	} else {
		return nil, ErrInvalidToken
	}
	name := user.Username
	if name == "" {
		name = "WindHub 用户 " + strconv.FormatInt(user.ID, 10)
	}
	return &RemoteIdentity{RemoteIssuer, strconv.FormatInt(user.ID, 10), RemoteAudience, g.Purpose, name, authorizationID, g.Expires}, nil
}

func (s *RemoteAuthorizationService) Authorize(ctx context.Context, userID int64, sessionID string, req RemoteAuthorizationRequest) (string, error) {
	if !req.Valid() {
		return "", ErrDesktopAuthorizationInvalid
	}
	u, err := s.users.GetByID(ctx, userID)
	if err != nil || u == nil {
		return "", ErrInvalidToken
	}
	g := remoteGrant{UserID: userID, Version: u.TokenVersion, SessionID: sessionID, Purpose: "browser", State: req.State, Challenge: req.Challenge}
	if _, err := s.identity(ctx, g); err != nil {
		return "", err
	}
	return s.put(ctx, "code", g, 3*time.Minute)
}

func (s *RemoteAuthorizationService) DesktopProof(ctx context.Context, key *APIKey, challenge string) (string, *RemoteIdentity, error) {
	if key == nil || !remoteChallenge.MatchString(challenge) {
		return "", nil, ErrDesktopAuthorizationInvalid
	}
	u, err := s.users.GetByID(ctx, key.UserID)
	if err != nil || u == nil {
		return "", nil, ErrInvalidToken
	}
	g := remoteGrant{UserID: u.ID, Version: u.TokenVersion, KeyID: key.ID, Purpose: "desktop", Challenge: challenge}
	identity, err := s.identity(ctx, g)
	if err != nil {
		return "", nil, err
	}
	code, err := s.put(ctx, "code", g, 3*time.Minute)
	return code, identity, err
}

// Validate PKCE before atomically consuming the code. Concurrent exchanges have
// exactly one winner; Redis outages fail closed and never issue a second grant.
func (s *RemoteAuthorizationService) Exchange(ctx context.Context, code, verifier, state, purpose string) (string, *RemoteIdentity, error) {
	g, raw, err := s.get(ctx, "code", code)
	if err != nil {
		return "", nil, err
	}
	hash := sha256.Sum256([]byte(verifier))
	actual := base64.RawURLEncoding.EncodeToString(hash[:])
	if !remoteVerifier.MatchString(verifier) || g.Purpose != purpose || g.State != state || subtle.ConstantTimeCompare([]byte(actual), []byte(g.Challenge)) != 1 {
		return "", nil, ErrDesktopAuthorizationInvalid
	}
	identity, err := s.identity(ctx, g)
	if err != nil {
		return "", nil, err
	}
	consumed, err := s.cache.GetDel(ctx, remoteCacheKey("code", code)).Result()
	if err != nil || consumed != raw {
		return "", nil, ErrInvalidToken
	}
	ttl := 8 * time.Hour
	if purpose == "desktop" {
		ttl = 5 * time.Minute
	}
	g.Challenge, g.State = "", ""
	token, err := s.put(ctx, "grant", g, ttl)
	identity.ExpiresAt = time.Now().Add(ttl).Unix()
	return token, identity, err
}

func (s *RemoteAuthorizationService) Inspect(ctx context.Context, token string) (*RemoteIdentity, error) {
	g, _, err := s.get(ctx, "grant", token)
	if err != nil {
		return nil, err
	}
	return s.identity(ctx, g)
}

func (s *RemoteAuthorizationService) Revoke(ctx context.Context, token string) error {
	if len(token) != 46 {
		return ErrInvalidToken
	}
	return s.cache.Del(ctx, remoteCacheKey("grant", token)).Err()
}
