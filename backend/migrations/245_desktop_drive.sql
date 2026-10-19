SET LOCAL lock_timeout = '2s';
SET LOCAL statement_timeout = '15s';

-- A personal drive belongs to the WindHub user, not a device key or tenant.
-- Raw objects live on the existing persistent data mount under file UUIDs;
-- user-provided display names never become filesystem paths.
CREATE TABLE IF NOT EXISTS desktop_drives (
    id UUID PRIMARY KEY,
    user_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    name VARCHAR(160) NOT NULL CHECK (length(btrim(name)) > 0),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (user_id, id)
);

CREATE TABLE IF NOT EXISTS desktop_drive_items (
    id UUID PRIMARY KEY,
    user_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    drive_id UUID NOT NULL,
    parent_id UUID,
    name VARCHAR(220) NOT NULL CHECK (length(btrim(name)) > 0),
    item_type VARCHAR(10) NOT NULL CHECK (item_type IN ('folder', 'file')),
    file_name VARCHAR(220) NOT NULL DEFAULT '',
    media_type VARCHAR(160) NOT NULL DEFAULT '',
    size_bytes BIGINT NOT NULL DEFAULT 0 CHECK (size_bytes >= 0),
    sha256 VARCHAR(64) NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (user_id, drive_id, id),
    FOREIGN KEY (user_id, drive_id) REFERENCES desktop_drives(user_id, id) ON DELETE CASCADE,
    FOREIGN KEY (user_id, drive_id, parent_id)
        REFERENCES desktop_drive_items(user_id, drive_id, id)
        ON DELETE CASCADE DEFERRABLE INITIALLY DEFERRED,
    CHECK (parent_id IS NULL OR parent_id <> id),
    CHECK ((item_type = 'folder' AND size_bytes = 0 AND sha256 = '')
        OR (item_type = 'file' AND sha256 ~ '^[0-9a-f]{64}$'))
);

CREATE INDEX IF NOT EXISTS desktop_drives_user_updated
    ON desktop_drives(user_id, updated_at DESC, id);
CREATE INDEX IF NOT EXISTS desktop_drive_items_user_parent
    ON desktop_drive_items(user_id, drive_id, parent_id, item_type, name, id);

-- Replays remain deterministic after deletions and API-key replacement. These
-- records intentionally have no TTL and no foreign key to mutable drive rows.
CREATE TABLE IF NOT EXISTS desktop_drive_requests (
    user_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    request_id UUID NOT NULL,
    fingerprint VARCHAR(64) NOT NULL CHECK (fingerprint ~ '^[0-9a-f]{64}$'),
    result JSONB NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (user_id, request_id)
);

-- Metadata removal and cleanup intent commit together. Failed filesystem
-- cleanup can be retried without resurrecting a file or deleting another user.
CREATE TABLE IF NOT EXISTS desktop_drive_garbage (
    user_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    file_id UUID NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (user_id, file_id)
);
