-- Module: identity (admin authentication).
CREATE TABLE identity_admins (
    id            TEXT PRIMARY KEY,
    email         TEXT NOT NULL UNIQUE,
    password_hash TEXT NOT NULL,
    created_at    TEXT NOT NULL
);

-- Opaque bearer sessions. Only the SHA-256 of the token is stored, so a
-- leaked database does not leak usable tokens.
CREATE TABLE identity_sessions (
    token_hash TEXT PRIMARY KEY,
    admin_id   TEXT NOT NULL REFERENCES identity_admins (id) ON DELETE CASCADE,
    created_at TEXT NOT NULL,
    expires_at TEXT NOT NULL
);
CREATE INDEX idx_identity_sessions_admin ON identity_sessions (admin_id);
