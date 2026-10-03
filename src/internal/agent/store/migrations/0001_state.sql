-- agent 本地库：插件私有 state。事件队列等由后续里程碑以新迁移加入。
CREATE TABLE instance_state (
    instance_id      TEXT PRIMARY KEY,
    plugin_id        TEXT NOT NULL,
    state            TEXT NOT NULL,
    -- content_hash 是写入 state 时实例配置内容的摘要（hub 下发的 content_hash），空表示未知。
    content_hash     TEXT NOT NULL DEFAULT '',
    updated_at       TEXT NOT NULL,
    last_assigned_at TEXT NOT NULL
);
