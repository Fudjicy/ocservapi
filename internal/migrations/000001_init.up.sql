CREATE TABLE IF NOT EXISTS system_instance (
    id BIGSERIAL PRIMARY KEY,
    installation_id TEXT NOT NULL UNIQUE,
    display_name TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    initialized_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    init_version TEXT NOT NULL DEFAULT 'v1'
);

CREATE TABLE IF NOT EXISTS system_settings (
    key TEXT PRIMARY KEY,
    value TEXT NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS users (
    id BIGSERIAL PRIMARY KEY,
    username TEXT NOT NULL UNIQUE,
    role TEXT NOT NULL CHECK (role IN ('owner', 'admin')),
    password_salt TEXT NOT NULL,
    password_hash TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS endpoints (
    id BIGSERIAL PRIMARY KEY,
    name TEXT NOT NULL UNIQUE,
    address TEXT NOT NULL,
    description TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS endpoint_admin_access (
    id BIGSERIAL PRIMARY KEY,
    user_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    endpoint_id BIGINT NOT NULL REFERENCES endpoints(id) ON DELETE CASCADE,
    can_view BOOLEAN NOT NULL DEFAULT FALSE,
    can_inspect BOOLEAN NOT NULL DEFAULT FALSE,
    can_deploy BOOLEAN NOT NULL DEFAULT FALSE,
    can_manage_users BOOLEAN NOT NULL DEFAULT FALSE,
    can_manage_certificates BOOLEAN NOT NULL DEFAULT FALSE,
    can_manage_endpoint BOOLEAN NOT NULL DEFAULT FALSE,
    UNIQUE(user_id, endpoint_id)
);

CREATE TABLE IF NOT EXISTS secrets (
    id BIGSERIAL PRIMARY KEY,
    name TEXT NOT NULL UNIQUE,
    encrypted_value BYTEA NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS ca_profiles (
    id BIGSERIAL PRIMARY KEY,
    name TEXT NOT NULL UNIQUE,
    kind TEXT NOT NULL,
    mode TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS endpoint_ca_bindings (
    id BIGSERIAL PRIMARY KEY,
    endpoint_id BIGINT NOT NULL REFERENCES endpoints(id) ON DELETE CASCADE,
    ca_profile_id BIGINT NOT NULL REFERENCES ca_profiles(id) ON DELETE CASCADE,
    binding_kind TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE(endpoint_id, ca_profile_id, binding_kind)
);

CREATE TABLE IF NOT EXISTS certificates (
    id BIGSERIAL PRIMARY KEY,
    endpoint_id BIGINT REFERENCES endpoints(id) ON DELETE SET NULL,
    common_name TEXT NOT NULL,
    cert_type TEXT NOT NULL,
    status TEXT NOT NULL DEFAULT 'issued',
    not_after TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS deployments (
    id BIGSERIAL PRIMARY KEY,
    endpoint_id BIGINT REFERENCES endpoints(id) ON DELETE SET NULL,
    created_by_user_id BIGINT REFERENCES users(id) ON DELETE SET NULL,
    operation_type TEXT NOT NULL,
    status TEXT NOT NULL,
    summary TEXT NOT NULL DEFAULT '',
    started_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    finished_at TIMESTAMPTZ
);

CREATE TABLE IF NOT EXISTS deployment_steps (
    id BIGSERIAL PRIMARY KEY,
    deployment_id BIGINT NOT NULL REFERENCES deployments(id) ON DELETE CASCADE,
    step_name TEXT NOT NULL,
    status TEXT NOT NULL,
    message TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS audit_events (
    id BIGSERIAL PRIMARY KEY,
    actor_user_id BIGINT REFERENCES users(id) ON DELETE SET NULL,
    endpoint_id BIGINT REFERENCES endpoints(id) ON DELETE SET NULL,
    action TEXT NOT NULL,
    result TEXT NOT NULL,
    message TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS sessions (
    id BIGSERIAL PRIMARY KEY,
    token_hash TEXT NOT NULL UNIQUE,
    user_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    expires_at TIMESTAMPTZ NOT NULL
);
