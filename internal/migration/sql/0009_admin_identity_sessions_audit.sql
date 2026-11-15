-- Step 9: Owner/Manager identity (Google sub), login transactions, user sessions, and administrative audit logging

-- 1. Application users (Google sub is immutable primary identity key)
CREATE TABLE IF NOT EXISTS public.app_user (
    id VARCHAR(64) PRIMARY KEY,
    google_sub VARCHAR(255) NOT NULL UNIQUE,
    email VARCHAR(255) NOT NULL,
    name VARCHAR(255) NOT NULL DEFAULT '',
    picture_url TEXT NOT NULL DEFAULT '',
    role VARCHAR(32) NOT NULL CHECK (role IN ('owner', 'manager', 'employee', 'customer')),
    is_active BOOLEAN NOT NULL DEFAULT TRUE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS app_user_email_idx ON public.app_user (email);
CREATE INDEX IF NOT EXISTS app_user_role_idx ON public.app_user (role);

-- 2. Short-lived single-use login transactions bound to browser OAuth state
CREATE TABLE IF NOT EXISTS public.login_transaction (
    id VARCHAR(64) PRIMARY KEY,
    state_hash VARCHAR(64) NOT NULL UNIQUE,
    nonce VARCHAR(64) NOT NULL,
    pkce_verifier VARCHAR(128) NOT NULL,
    browser_binding_hash VARCHAR(64) NOT NULL,
    return_url TEXT NOT NULL DEFAULT '/',
    expires_at TIMESTAMPTZ NOT NULL,
    used_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS login_transaction_lookup_idx 
    ON public.login_transaction (state_hash) 
    WHERE used_at IS NULL;

-- 3. Authoritative server-side user sessions
CREATE TABLE IF NOT EXISTS public.user_session (
    id VARCHAR(64) PRIMARY KEY,
    user_id VARCHAR(64) NOT NULL REFERENCES public.app_user(id) ON DELETE CASCADE,
    token_hash VARCHAR(64) NOT NULL UNIQUE,
    csrf_secret VARCHAR(64) NOT NULL,
    ip_address VARCHAR(45) NOT NULL,
    user_agent TEXT NOT NULL DEFAULT '',
    is_revoked BOOLEAN NOT NULL DEFAULT FALSE,
    expires_at TIMESTAMPTZ NOT NULL,
    max_expires_at TIMESTAMPTZ NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    last_seen_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS user_session_lookup_idx
    ON public.user_session (token_hash)
    WHERE is_revoked = FALSE;

CREATE INDEX IF NOT EXISTS user_session_user_idx
    ON public.user_session (user_id);

-- 4. Append-only administrative audit log
CREATE TABLE IF NOT EXISTS public.admin_audit_log (
    id VARCHAR(64) PRIMARY KEY,
    user_id VARCHAR(64) REFERENCES public.app_user(id) ON DELETE SET NULL,
    actor_role VARCHAR(32) NOT NULL,
    action VARCHAR(64) NOT NULL,
    resource VARCHAR(64) NOT NULL,
    resource_id VARCHAR(64) NOT NULL DEFAULT '',
    ip_address VARCHAR(45) NOT NULL,
    metadata JSONB NOT NULL DEFAULT '{}'::jsonb,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS admin_audit_log_created_idx
    ON public.admin_audit_log (created_at DESC);
CREATE INDEX IF NOT EXISTS admin_audit_log_user_idx
    ON public.admin_audit_log (user_id);
CREATE INDEX IF NOT EXISTS admin_audit_log_resource_idx
    ON public.admin_audit_log (resource, resource_id);

-- 5. Runtime permissions (Append-only on admin_audit_log)
DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'bliss_runtime') THEN
        GRANT SELECT, INSERT, UPDATE ON public.app_user TO bliss_runtime;
        GRANT SELECT, INSERT, UPDATE, DELETE ON public.login_transaction TO bliss_runtime;
        GRANT SELECT, INSERT, UPDATE, DELETE ON public.user_session TO bliss_runtime;
        GRANT SELECT, INSERT ON public.admin_audit_log TO bliss_runtime;
    END IF;
END $$;
