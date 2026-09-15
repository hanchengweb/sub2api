-- Human identities keep their existing behavior. Organization bindings are
-- reserved even after soft deletion so an external organization cannot be
-- silently reassigned to a new account and lose its billing history.
-- Fail and roll back rather than queue behind live traffic indefinitely.
-- The runner wraps this entire file in one transaction. Use an ordinary CHECK:
-- NOT VALID followed by VALIDATE here cannot release ACCESS EXCLUSIVE early.
-- Hangzhou had 17 users at the 2026-09-15 preflight; no two-phase rollout.
-- lock_timeout bounds each lock acquisition, not total transaction/traffic delay.
-- statement_timeout bounds each statement; locks already acquired last to COMMIT.
-- SET LOCAL is scoped to the migration runner's transaction.
SET LOCAL lock_timeout = '2s';
SET LOCAL statement_timeout = '15s';

ALTER TABLE users
    ADD COLUMN account_type VARCHAR(32) NOT NULL DEFAULT 'personal',
    ADD COLUMN organization_issuer VARCHAR(80) NOT NULL DEFAULT '',
    ADD COLUMN organization_id VARCHAR(160) NOT NULL DEFAULT '',
    ADD COLUMN organization_environment VARCHAR(20) NOT NULL DEFAULT '';

ALTER TABLE users ADD CONSTRAINT users_organization_identity_valid CHECK (
    (account_type = 'personal' AND organization_issuer = ''
        AND organization_id = '' AND organization_environment = '')
    OR
    (account_type = 'organization_service' AND role = 'user'
        AND btrim(organization_issuer) <> '' AND btrim(organization_id) <> ''
        AND organization_environment IN ('test', 'production'))
);

CREATE UNIQUE INDEX users_organization_identity_unique
    ON users (organization_issuer, organization_environment, organization_id)
    WHERE account_type = 'organization_service';
