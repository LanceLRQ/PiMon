-- 插件运行时相关表。分支内各任务在文件末尾追加各自的表。

-- 代理：全局代理列表，插件实例按 id 引用。
-- auth_enc 为 secret.Box 加密后的认证（空串表示无认证），永不明文入库。
CREATE TABLE proxies (
    id         TEXT PRIMARY KEY,
    name       TEXT NOT NULL UNIQUE,
    scheme     TEXT NOT NULL CHECK (scheme IN ('http', 'https', 'socks5', 'socks5h')),
    address    TEXT NOT NULL,
    remote_dns INTEGER NOT NULL DEFAULT 0 CHECK (remote_dns IN (0, 1)),
    location   TEXT NOT NULL DEFAULT 'any' CHECK (location IN ('hub', 'lan', 'any')),
    auth_enc   TEXT NOT NULL DEFAULT '',
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL
);

-- 插件 manifest：按（机器, 插件, 版本）存库，本期机器固定为 hub。
-- available 为 0 表示该版本当前不可用（exec 插件被删除、或已换成别的版本），
-- 记录保留，供实例仓库判定"插件消失 → 引用失效"。
CREATE TABLE manifests (
    machine       TEXT NOT NULL,
    plugin_id     TEXT NOT NULL,
    version       TEXT NOT NULL,
    origin        TEXT NOT NULL CHECK (origin IN ('builtin', 'exec')),
    manifest_json TEXT NOT NULL,
    available     INTEGER NOT NULL DEFAULT 1 CHECK (available IN (0, 1)),
    first_seen_at TEXT NOT NULL,
    updated_at    TEXT NOT NULL,
    PRIMARY KEY (machine, plugin_id, version)
);

-- 插件实例：一个实例 = 插件 + 配置 + 运行位置 + 刷新间隔覆盖。
-- config_json 是普通配置（密钥字段已剥离，kv 密钥值以 null 占位）；
-- secrets_enc 是 secret.Box 加密后的"密钥路径 → 值" JSON，空串表示没有密钥。
-- interval_seconds 为 0 表示沿用 manifest 的默认间隔。
-- proxy_id 是配置里 proxy 类型字段的取值（空表示直连），冗余存放以便按代理查引用。
-- config_hash 是 plugin_id、普通配置与密钥密文的摘要，内容变化时用来判定是否重置当前状态。
CREATE TABLE plugin_instances (
    id               TEXT PRIMARY KEY,
    plugin_id        TEXT NOT NULL,
    name             TEXT NOT NULL,
    machine          TEXT NOT NULL DEFAULT 'hub',
    config_json      TEXT NOT NULL,
    secrets_enc      TEXT NOT NULL DEFAULT '',
    interval_seconds INTEGER NOT NULL DEFAULT 0 CHECK (interval_seconds >= 0),
    paused           INTEGER NOT NULL DEFAULT 0 CHECK (paused IN (0, 1)),
    proxy_id         TEXT NOT NULL DEFAULT '',
    config_hash      TEXT NOT NULL,
    created_at       TEXT NOT NULL,
    updated_at       TEXT NOT NULL
);
CREATE INDEX plugin_instances_proxy ON plugin_instances (proxy_id) WHERE proxy_id <> '';

-- 实例当前状态：内存为准，每 30 秒落盘，重启恢复。
-- report_json 含插件私有 state；last_success_at 为 Unix 毫秒，0 表示从未成功。
CREATE TABLE instance_state (
    instance_id     TEXT PRIMARY KEY REFERENCES plugin_instances (id) ON DELETE CASCADE,
    report_json     TEXT NOT NULL DEFAULT '',
    last_success_at INTEGER NOT NULL DEFAULT 0,
    last_error      TEXT NOT NULL DEFAULT '',
    failures        INTEGER NOT NULL DEFAULT 0,
    updated_at      TEXT NOT NULL
);
