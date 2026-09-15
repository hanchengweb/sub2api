-- Human identities keep their existing behavior. Organization bindings are
-- reserved even after soft deletion so an external organization cannot be
-- silently reassigned to a new account and lose its billing history.
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
