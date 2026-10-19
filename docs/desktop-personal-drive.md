# Desktop personal cloud drive

Personal cloud drives belong to the WindHub user. Changing devices, rotating a
personal API key or switching the desktop's organization does not change their
owner. The desktop must retain and explicitly use its separate personal
connection for these requests; tenant tokens and organization service keys are
not accepted.

## API and authentication

All routes use `APIKeyAuth` and `ValidateDesktopRechargeKey`, followed by the
drive-specific ordinary personal group `4` and user-identity check. Expired or
disabled keys, inactive users/groups, restricted keys, subscription connections
and organization service users remain rejected. Only the exact drive route and
method pairs bypass model-balance enforcement; zero balance does not prevent
managing existing personal files. Model requests retain their billing checks.

All JSON responses use the existing `{code,message,data}` envelope.

| Method | Path below `/v1/desktop/drive` | Purpose |
| --- | --- | --- |
| GET | empty | `account`, `resourceFolders`, `quota` |
| POST | empty | Create a drive with `{name,clientRequestId}` |
| GET | `/:resource_id` | List direct children with `parent_id`, `offset`, `limit` |
| POST | `/:resource_id/folders` | Create with `{name,parentId?,clientRequestId}` |
| POST | `/:resource_id/upload` | Multipart repeated `files`, optional `parent_id` |
| GET | `/:resource_id/items/:item_id` | Metadata and bounded text content |
| GET | `/:resource_id/items/:item_id/original` | Private attachment bytes, supports HTTP ranges |
| PATCH / DELETE | `/:resource_id` | Rename with `{name}` / delete the drive |
| PATCH / DELETE | `/:resource_id/items/:item_id` | Rename with `{name}` / recursively delete a subtree |

Writes require a nonzero canonical UUID `Idempotency-Key`. If a JSON
`clientRequestId` is supplied, it must equal that header. Reuse the same key and
identical payload after an unknown outcome; a different payload returns `409`.
The fingerprint binds the user, operation, drive/item IDs, parent, names, file
sizes and SHA-256 digests. Requests are remembered by user rather than device.
An upload whose permanent receipt already exists hashes the bounded retry body
without writing another temporary copy, so a confirmed upload can still replay
when account capacity is full or new writes are disabled by the disk floor.

Migration `245_desktop_drive.sql` adds metadata, permanent request receipts and
a deletion outbox. The existing idempotency coordinator stores only a small
commit acknowledgement. Complete file results replay from the permanent
owner-bound receipt, preserving long names and batches above the coordinator's
generic response-size limit. Deleting a drive or a file does not remove its
receipts, so retries cannot recreate it after the general cache expires.

## Storage and limits

Objects use the existing persistent `cfg.Pricing.DataDir` mount:
`desktop-drive/<WindHub user ID>/<file UUID>`. User-supplied names never become
filesystem paths, and the API returns neither storage paths nor public URLs.
`os.Root`, regular-file checks and UUID-only names contain reads and cleanup
within the authenticated user's directory. Symlink storage directories are
rejected. SHA-256 and file fsync precede atomic rename; on Linux, directory fsync
also precedes metadata commit.

Initial limits are 1 GiB per account, 50 MiB per file, 80 MiB / 100 files per
upload, 10,000 total drives/folders/files per account, and a 5 GiB free-disk
floor. A user-scoped PostgreSQL transaction/advisory lock serializes metadata
mutations and quota decisions across requests. Each upload is streamed through
private temporary files; pending physical bytes also count against staging
capacity. On the current single-instance deployment, upload admission permits
one request per user and four globally, returning `429` for additional requests.
Active stages reserve their worst-case 50 MiB before starting disk writes.
Multi-instance deployments must replace this process-local scratch admission
with coordinated admission before sharing the mount.

Deletion commits metadata removal and a file-cleanup outbox together. Catalog
reads and writes run bounded maintenance even when the disk floor rejects new
uploads. Maintenance drains multiple deletion batches and, within a one-second
budget, rotates through at most 10,000 entries looking for regular files older
than one hour. It removes only valid upload-temporary names or UUID objects
confirmed absent from the same user's metadata. Live objects, young files,
symlinks and files belonging to an active upload are preserved. A DB outage
retains published files for a safe retry. Cleanup is request-triggered; there is
no unattended background or scheduled garbage collector.

Plain UTF-8 TXT, Markdown, CSV, JSON and safe HTML text extraction is bounded to
2 MiB input and 200,000 Unicode characters per page. HTML scripts/styles are
excluded and nothing executes or fetches a network resource. Unsupported or
binary formats return empty `content` and `contentUnavailable: true`; they are
never described as extracted text. The desktop can download the original and
reuse its existing Office/PDF viewers. This backend adds no general document
engine or new cloud resource.

## Local verification and release boundary

Run focused tests from `backend`:

```text
go test ./internal/service ./internal/repository ./internal/server/routes -run '^TestDesktop(Drive|Recharge)' -count=1
go test -tags=unit ./internal/server/middleware -run '^TestDesktopDrive' -count=1
go vet ./internal/service ./internal/repository ./internal/handler ./internal/server/...
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath ./cmd/server
```

The two opt-in PostgreSQL tests require `WINDHUB_DRIVE_PG_TEST=1` and a dedicated
loopback cluster at `127.0.0.1:55473` as `drive_test`. They never accept an
application DSN. Each creates and drops only its own random database. They
verify migration upgrade/rollback, existing-account preservation, owner and
parent foreign keys, recursive deletion, durable replay, actual authenticated
HTTP multipart/download and lossless 100-file Unicode response replay.

These checks establish local implementation behavior. Production requires a
reviewed main commit, existing deployment workflow, migration/runtime identity
checks and authenticated desktop readback. This task does not deploy or change
production data, DNS, secrets, cloud resources or account permissions.

Rollback the application image through the existing release workflow while
retaining migration 245's additive tables and `desktop-drive` objects. Do not
drop these tables or remove user files during rollback; the previous image
simply does not register the new routes. The migration changes no existing
credential, balance, tenant or payment table data.
