-- M2e：远端备份目标配置与上传记录。本文件一次写完，迁移提交后不可再改。
-- SMB 与 WebDAV 的字段分列保存，切换类型不丢另一类型已填的值；密码与备份口令为 secret.Box 加密后的密文。
-- 时间列为 TEXT，经 store.FormatTime 写入。

-- 远端备份目标：单行表，没有行表示从未保存过（读取时按默认值）。
CREATE TABLE backup_remote (
    id                  INTEGER PRIMARY KEY CHECK (id = 1),
    enabled             INTEGER NOT NULL DEFAULT 0 CHECK (enabled IN (0, 1)),
    type                TEXT NOT NULL DEFAULT 'smb' CHECK (type IN ('', 'smb', 'webdav')),
    smb_host            TEXT NOT NULL DEFAULT '',
    smb_port            INTEGER NOT NULL DEFAULT 445,
    smb_share           TEXT NOT NULL DEFAULT '',
    smb_dir             TEXT NOT NULL DEFAULT '',
    smb_username        TEXT NOT NULL DEFAULT '',
    smb_password_enc    TEXT NOT NULL DEFAULT '',
    smb_domain          TEXT NOT NULL DEFAULT '',
    webdav_url          TEXT NOT NULL DEFAULT '',
    webdav_dir          TEXT NOT NULL DEFAULT '',
    webdav_username     TEXT NOT NULL DEFAULT '',
    webdav_password_enc TEXT NOT NULL DEFAULT '',
    passphrase_enc      TEXT NOT NULL DEFAULT '',
    keep                INTEGER NOT NULL DEFAULT 30 CHECK (keep BETWEEN 1 AND 365),
    updated_at          TEXT NOT NULL
);

-- 上传记录：只保留最近 90 条，由写入方在插入后清理。
-- size 是写到远端的密文字节数；success 为 0 时 reason 给出失败原因（不含密码）。
CREATE TABLE backup_uploads (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    name        TEXT NOT NULL,
    target_type TEXT NOT NULL,
    started_at  TEXT NOT NULL,
    finished_at TEXT NOT NULL,
    size        INTEGER NOT NULL DEFAULT 0,
    success     INTEGER NOT NULL CHECK (success IN (0, 1)),
    reason      TEXT NOT NULL DEFAULT ''
);
