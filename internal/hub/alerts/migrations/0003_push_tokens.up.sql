-- The app installations that receive a household's alerts as push
-- notifications (issue #49). A token is what Expo's push service knows the
-- installation by; it identifies a phone's app, not a person, and is
-- dropped when the provider says it is gone, when the member removes it
-- (sign-out, alerts turned off), or when the member leaves the household.
CREATE TABLE IF NOT EXISTS hub_push_tokens (
    tenant_id  UUID        NOT NULL,
    user_id    UUID        NOT NULL,
    token      TEXT        NOT NULL,
    platform   TEXT        NOT NULL CHECK (platform IN ('android', 'ios')),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (tenant_id, token)
);

CREATE INDEX IF NOT EXISTS hub_push_tokens_token_idx ON hub_push_tokens (token);
CREATE INDEX IF NOT EXISTS hub_push_tokens_member_idx ON hub_push_tokens (tenant_id, user_id);
