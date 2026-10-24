SET LOCAL lock_timeout = '2s';
SET LOCAL statement_timeout = '15s';

-- Personal collaboration is independent of organizations and local workspaces.
CREATE TABLE desktop_collaboration_rooms (
    id UUID PRIMARY KEY,
    owner_id BIGINT NOT NULL REFERENCES users(id),
    name VARCHAR(160) NOT NULL,
    project_id UUID UNIQUE,
    project_name VARCHAR(160) NOT NULL DEFAULT '',
    next_seq BIGINT NOT NULL DEFAULT 0,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE TABLE desktop_collaboration_members (
    room_id UUID NOT NULL REFERENCES desktop_collaboration_rooms(id),
    user_id BIGINT NOT NULL REFERENCES users(id),
    active BOOLEAN NOT NULL DEFAULT true,
    joined_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY(room_id,user_id)
);
CREATE INDEX desktop_collaboration_member_user ON desktop_collaboration_members(user_id,room_id) WHERE active;
CREATE TABLE desktop_collaboration_agents (
    id UUID PRIMARY KEY,
    room_id UUID NOT NULL REFERENCES desktop_collaboration_rooms(id),
    name VARCHAR(160) NOT NULL,
    instructions TEXT NOT NULL,
    model VARCHAR(200) NOT NULL,
    active BOOLEAN NOT NULL DEFAULT true,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE(room_id,id)
);
CREATE TABLE desktop_collaboration_invites (
    id UUID PRIMARY KEY,
    room_id UUID NOT NULL REFERENCES desktop_collaboration_rooms(id),
    token_hash VARCHAR(64) NOT NULL UNIQUE,
    expires_at TIMESTAMPTZ NOT NULL,
    revoked BOOLEAN NOT NULL DEFAULT false
);
CREATE TABLE desktop_collaboration_messages (
    id UUID PRIMARY KEY,
    room_id UUID NOT NULL REFERENCES desktop_collaboration_rooms(id),
    seq BIGINT NOT NULL,
    kind VARCHAR(10) NOT NULL CHECK(kind IN ('human','agent','system')),
    author_id TEXT NOT NULL,
    author_name VARCHAR(160) NOT NULL,
    text TEXT NOT NULL,
    reply_to_id UUID,
    run_id UUID UNIQUE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE(room_id,seq),
    UNIQUE(room_id,id),
    FOREIGN KEY(room_id,reply_to_id) REFERENCES desktop_collaboration_messages(room_id,id)
);
CREATE TABLE desktop_collaboration_runs (
    id UUID PRIMARY KEY,
    room_id UUID NOT NULL REFERENCES desktop_collaboration_rooms(id),
    message_id UUID NOT NULL,
    agent_id UUID NOT NULL,
    agent_name VARCHAR(160) NOT NULL,
    instructions TEXT NOT NULL,
    model VARCHAR(200) NOT NULL,
    payer_id BIGINT NOT NULL REFERENCES users(id),
    key_id BIGINT NOT NULL REFERENCES api_keys(id),
    status VARCHAR(24) NOT NULL CHECK(status IN ('queued','running','completed','failed','outcome_unknown')),
    error_code VARCHAR(80) NOT NULL DEFAULT '',
    error_message VARCHAR(240) NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    started_at TIMESTAMPTZ,
    finished_at TIMESTAMPTZ,
    UNIQUE(message_id,agent_id),
    FOREIGN KEY(room_id,message_id) REFERENCES desktop_collaboration_messages(room_id,id),
    FOREIGN KEY(room_id,agent_id) REFERENCES desktop_collaboration_agents(room_id,id)
);
CREATE INDEX desktop_collaboration_run_queue ON desktop_collaboration_runs(created_at,id) WHERE status='queued';
CREATE INDEX desktop_collaboration_run_room ON desktop_collaboration_runs(room_id,created_at DESC);
CREATE TABLE desktop_collaboration_tasks (
    id UUID PRIMARY KEY,
    room_id UUID NOT NULL REFERENCES desktop_collaboration_rooms(id),
    title VARCHAR(240) NOT NULL,
    status VARCHAR(10) NOT NULL CHECK(status IN ('todo','doing','done')),
    assignee_id BIGINT,
    revision BIGINT NOT NULL DEFAULT 1,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    FOREIGN KEY(room_id,assignee_id) REFERENCES desktop_collaboration_members(room_id,user_id)
);
CREATE TABLE desktop_collaboration_requests (
    user_id BIGINT NOT NULL REFERENCES users(id),
    request_id UUID NOT NULL,
    fingerprint VARCHAR(64) NOT NULL,
    result JSONB NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY(user_id,request_id)
);

-- Explicit room uploads are shared with current members only. Binary content
-- is never read from a private-drive row or a client-supplied filesystem path.
CREATE TABLE desktop_collaboration_assets (
    id UUID PRIMARY KEY,
    room_id UUID NOT NULL REFERENCES desktop_collaboration_rooms(id),
    uploader_id BIGINT NOT NULL REFERENCES users(id),
    name VARCHAR(220) NOT NULL,
    media_type VARCHAR(160) NOT NULL,
    size_bytes BIGINT NOT NULL CHECK(size_bytes BETWEEN 0 AND 10485760),
    sha256 VARCHAR(64) NOT NULL,
    content BYTEA NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE(room_id,id),
    CHECK(octet_length(content)=size_bytes)
);
CREATE TABLE desktop_collaboration_message_assets (
    room_id UUID NOT NULL,
    message_id UUID NOT NULL,
    asset_id UUID NOT NULL,
	context_note VARCHAR(160) NOT NULL DEFAULT '',
    PRIMARY KEY(message_id,asset_id),
    FOREIGN KEY(room_id,message_id) REFERENCES desktop_collaboration_messages(room_id,id),
    FOREIGN KEY(room_id,asset_id) REFERENCES desktop_collaboration_assets(room_id,id)
);
