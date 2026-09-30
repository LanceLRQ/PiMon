-- 全局设置：value 为 JSON
CREATE TABLE settings (
    key   TEXT PRIMARY KEY,
    value TEXT NOT NULL
);

-- 管理员：单行
CREATE TABLE admin (
    id            INTEGER PRIMARY KEY CHECK (id = 1),
    password_hash TEXT NOT NULL,
    created_at    TEXT NOT NULL,
    updated_at    TEXT NOT NULL
);

-- 会话：不记录最后活跃时间，减少 SD 卡写入
CREATE TABLE sessions (
    token_hash TEXT PRIMARY KEY,
    kind       TEXT NOT NULL CHECK (kind IN ('admin', 'screen')),
    created_at TEXT NOT NULL,
    expires_at TEXT
);
CREATE INDEX idx_sessions_kind ON sessions (kind);

-- 首次设置码：单行
CREATE TABLE setup_codes (
    id         INTEGER PRIMARY KEY CHECK (id = 1),
    code_hash  TEXT NOT NULL,
    expires_at TEXT NOT NULL
);

-- 屏幕令牌：单行
CREATE TABLE screen_tokens (
    id         INTEGER PRIMARY KEY CHECK (id = 1),
    token_hash TEXT NOT NULL,
    created_at TEXT NOT NULL
);

-- 元信息：如上次运行的应用版本（app_version）
CREATE TABLE meta (
    key   TEXT PRIMARY KEY,
    value TEXT NOT NULL
);
