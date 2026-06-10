CREATE TABLE IF NOT EXISTS settings (
    key   TEXT PRIMARY KEY,
    value TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS devices (
    id            TEXT PRIMARY KEY,
    hostname      TEXT UNIQUE NOT NULL,
    os            TEXT NOT NULL,
    wg_public_key TEXT UNIQUE NOT NULL,
    mesh_ip       TEXT UNIQUE NOT NULL,
    cert_pem      TEXT,
    key_pem       TEXT,
    last_seen     DATETIME,
    last_endpoint TEXT,
    agent_version TEXT,
    created_at    DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS preauth_keys (
    key        TEXT PRIMARY KEY,
    label      TEXT,
    reusable   BOOLEAN NOT NULL DEFAULT 0,
    expiry     DATETIME NOT NULL,
    used_at    DATETIME,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS services (
    id          TEXT PRIMARY KEY,
    device_id   TEXT NOT NULL REFERENCES devices(id) ON DELETE CASCADE,
    name        TEXT NOT NULL,
    port        INTEGER NOT NULL,
    target_addr TEXT NOT NULL DEFAULT '',
    UNIQUE(device_id, name)
);

-- service_access stores the resolved ACL: which WG pubkey may connect to a service.
-- Stored pubkey is immutable at grant time — re-enrollment with a new key revokes access.
CREATE TABLE IF NOT EXISTS service_access (
    service_id      TEXT NOT NULL REFERENCES services(id) ON DELETE CASCADE,
    allowed_pubkey  TEXT NOT NULL,
    PRIMARY KEY (service_id, allowed_pubkey)
);

CREATE TABLE IF NOT EXISTS audit_log (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    event      TEXT NOT NULL,
    device_id  TEXT,
    metadata   TEXT,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);
