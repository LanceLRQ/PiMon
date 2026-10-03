-- agent 本地库：插件私有 state。事件队列等由后续里程碑以新迁移加入。
CREATE TABLE instance_state (
    instance_id      TEXT PRIMARY KEY,
    plugin_id        TEXT NOT NULL,
    state            TEXT NOT NULL,
    updated_at       TEXT NOT NULL,
    last_assigned_at TEXT NOT NULL
);
