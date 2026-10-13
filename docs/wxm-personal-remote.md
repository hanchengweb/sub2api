# Personal mobile remote authorization

The fixed client `wxm-mobile-remote` authorizes only the task relay at
`https://app.wemoreai.com/remote/v1`. Its exact browser callback is
`https://app.wemoreai.com/remote/v1/personal/callback`.

- A logged-in WindHub user explicitly approves the browser authorization page.
  `/api/v1/auth/remote/authorize` returns a 3-minute, one-use code bound to
  S256 PKCE and state. `/exchange` returns an opaque 8-hour browser grant.
- A desktop model key goes only to `/v1/desktop/remote/proof`. It creates a
  PKCE-bound, one-use proof of key ownership, never a user login token. The
  relay exchanges this for a 5-minute desktop grant, renewed by the desktop.
- `/inspect` checks purpose, account activity, current token version and the
  live WindHub browser refresh-token family or desktop key. Revoking the
  original session/key, changing password, or disabling the account therefore
  invalidates the remote grant. `/revoke` removes the named grant immediately.
- Opaque tokens live hashed in Redis, in a dedicated namespace. No new signing
  secret, shared user table, general JWT audience or cross-system refresh token
  is introduced. Codes and grants are different Redis records. Concurrent code
  consumption uses GETDEL (Redis 6.2+); failure denies authorization.
- The relay additionally checks exact owner identity and requires a physical
  desktop confirmation with matching digits before dispatching tasks.

The existing Fengxing desktop OAuth client remains separate and unchanged.
Tests: `go test -tags=unit ./internal/service ./internal/handler
./internal/server/middleware ./internal/server/routes -run
'TestRemoteAuthorization|Test.*Desktop|Test.*JWT'`; frontend auth store, OAuth
and desktopAuthorization Vitest; production frontend build.
