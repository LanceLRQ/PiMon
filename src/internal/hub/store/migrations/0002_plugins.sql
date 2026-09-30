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
