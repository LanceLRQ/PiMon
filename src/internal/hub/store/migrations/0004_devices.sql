-- M2b：设备与配对码。本文件一次写完（含后续子任务才写逻辑的列），迁移提交后不可再改。
-- 不加外键：plugin_instances.machine 可能是字面量 hub，也可能指向已吊销的设备（引用失效）；manifests.machine 同理。
-- presence、连接错误、时钟偏差都是内存运行时值，不建列。

-- 设备（不含中枢本机，中枢继续用字面量 machine = 'hub'）。
-- confirmed_at 为空表示已签发凭据但从未 hello；replaces 是重新配对时的旧设备 ID，确认后清空。
CREATE TABLE devices (
    id                  TEXT PRIMARY KEY,
    name                TEXT NOT NULL UNIQUE,
    role                TEXT NOT NULL CHECK (role IN ('server', 'nas', 'dev', 'vps')),
    direction           TEXT NOT NULL CHECK (direction IN ('dial_in', 'dial_out')),
    address             TEXT NOT NULL DEFAULT '',
    hostname            TEXT NOT NULL DEFAULT '',
    proxy_id            TEXT NOT NULL DEFAULT '',
    allow_offline       INTEGER NOT NULL DEFAULT 0 CHECK (allow_offline IN (0, 1)),
    maintenance         INTEGER NOT NULL DEFAULT 0 CHECK (maintenance IN (0, 1)),
    credential_hash     TEXT NOT NULL DEFAULT '',
    credential_enc      TEXT NOT NULL DEFAULT '',
    pin                 TEXT NOT NULL DEFAULT '',
    agent_version       TEXT NOT NULL DEFAULT '',
    os                  TEXT NOT NULL DEFAULT '',
    arch                TEXT NOT NULL DEFAULT '',
    protocol_version    INTEGER NOT NULL DEFAULT 0,
    incompatible_reason TEXT NOT NULL DEFAULT '',
    required_min        INTEGER NOT NULL DEFAULT 0,
    auto_host_metrics   INTEGER NOT NULL DEFAULT 0 CHECK (auto_host_metrics IN (0, 1)),
    replaces            TEXT NOT NULL DEFAULT '',
    confirmed_at        TEXT,
    last_seen_at        TEXT,
    created_at          TEXT NOT NULL,
    updated_at          TEXT NOT NULL
);

-- 拨入配对码：只存规范化后的 sha256，一次性，过期由 devices 包清理。
CREATE TABLE pairing_codes (
    code_hash         TEXT PRIMARY KEY,
    expires_at        TEXT NOT NULL,
    name              TEXT NOT NULL DEFAULT '',
    role              TEXT NOT NULL DEFAULT 'server',
    allow_offline     INTEGER NOT NULL DEFAULT 0,
    auto_host_metrics INTEGER NOT NULL DEFAULT 1,
    repair_device_id  TEXT NOT NULL DEFAULT '',
    created_at        TEXT NOT NULL
);
CREATE INDEX pairing_codes_expires ON pairing_codes (expires_at);

CREATE INDEX plugin_instances_machine ON plugin_instances (machine);
