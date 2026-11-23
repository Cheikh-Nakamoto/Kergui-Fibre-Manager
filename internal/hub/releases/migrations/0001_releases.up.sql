-- The app's releases (issue #71): the builds CI declares, the minimum build
-- per platform and channel, and the last build each anonymous installation
-- reported, for the per-version counts. An installation id is random and
-- generated once by the app: no phone number, no household, no account.

CREATE TABLE IF NOT EXISTS hub_releases (
    id           UUID PRIMARY KEY,
    platform     TEXT        NOT NULL,
    channel      TEXT        NOT NULL,
    version_name TEXT        NOT NULL,
    build        BIGINT      NOT NULL CHECK (build > 0),
    url          TEXT        NOT NULL,
    sha256       TEXT        NOT NULL,
    size         BIGINT      NOT NULL CHECK (size > 0),
    notes        TEXT        NOT NULL DEFAULT '',
    state        TEXT        NOT NULL CHECK (state IN ('pending', 'published', 'withdrawn')),
    declared_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    published_at TIMESTAMPTZ,
    withdrawn_at TIMESTAMPTZ,
    -- Declaring a build twice never makes two rows.
    UNIQUE (platform, channel, build)
);

CREATE TABLE IF NOT EXISTS hub_release_minimums (
    platform TEXT        NOT NULL,
    channel  TEXT        NOT NULL,
    build    BIGINT      NOT NULL CHECK (build >= 0),
    set_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (platform, channel)
);

CREATE TABLE IF NOT EXISTS hub_release_installs (
    platform   TEXT        NOT NULL,
    channel    TEXT        NOT NULL,
    install_id TEXT        NOT NULL,
    build      BIGINT      NOT NULL,
    seen_at    TIMESTAMPTZ NOT NULL,
    PRIMARY KEY (platform, channel, install_id)
);

CREATE INDEX IF NOT EXISTS hub_release_installs_seen_idx ON hub_release_installs (seen_at);
