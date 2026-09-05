# WindHub Stable Baseline

Status: established from the verified WindHub production runtime. This
baseline records source identity and the gateway homepage asset. It does not
perform a deployment, database migration, balance change, payment change, or
organization compute-pool change.

## Source authority

- Official open-source upstream: https://github.com/Wei-Shaw/sub2api
- WindHub owner fork: https://github.com/hanchengweb/sub2api
- Verified runtime source commit: `33e3670670ae39510f410c2e37102f60b6b75630`
- Source branch at capture: `codex/windhub-release-20260831`
- Commit subject: `fix: refine WindHub dashboard domestic model display`
- Parent commit: `05f55bf356c2717137ddeba048a82aa1ab57043a`

The runtime commit is a custom WindHub release commit based on the official
open-source project. The product worktrees for later model-plaza and pricing
work are not this runtime baseline and must not be used as a release source
without a separate review and merge.

## Verified production identity

- Public target: `https://windhub.online/`
- Runtime image: `sub2api:0.1.180-wxm1-windhub-dashboard-domestic-20260901`
- Runtime image digest: `sha256:69082539477038c0733225364fa3a636d60fe43fb3c8d8a129637ce3bb721d3d`
- Runtime-reported commit: `33e3670670ae39510f410c2e37102f60b6b75630`
- Runtime state at capture: `running|healthy`
- Database migrations in this baseline: none
- Data volumes changed by this baseline: none

## Gateway homepage asset

`deploy/gateway/windhub/windhub-home.html` is the byte-for-byte copy of the
production-verified gateway homepage, including the `/logo.ico` reference.

- SHA256: `24aeacfe793e25c1988d57f271e507d7d3827c1d05277a8b48efa92e3d5591fc`
- The tracked `frontend/public/windhub-home.html` remains the application
  frontend asset. The gateway copy is tracked separately because these are
  different serving paths and different production inputs.

The stable gateway icon is `deploy/gateway/windhub/logo.ico`.

- SHA256: `26439e31abb1415985d04674ba39f9783a039c87a78d862c1fac68e9b8e7fb8a`
- The exact Nginx route is `deploy/gateway/WINDHUB_LOGO_LOCATION.conf` and
  must be included inside the `windhub.online` TLS server block. Without this
  route, `/logo.ico` falls through to the application SPA and returns HTML.

## Stable references

- Baseline branch: `codex/windhub-stable-baseline-20260904`
- `windhub-runtime-20260901` must point to the verified runtime commit above.
- `windhub-stable-20260901` must point to the commit containing this record
  and the gateway asset.

## Rule for future work

1. Fetch the WindHub owner fork and start from the stable branch or stable tag.
2. Create a named `codex/` task branch; do not continue from an old worktree.
3. Keep product changes, provider configuration, and production configuration
   separate until the relevant acceptance check passes.
4. Release only from a reviewed commit whose source, package, runtime, and
   public readback can be matched.

## Rollback

For an application rollback, redeploy the retained runtime image and digest
listed above. For the gateway homepage, restore the retained previous gateway
copy in the protected operations environment. This baseline has no database
migration, so its code rollback does not require a schema rollback.
