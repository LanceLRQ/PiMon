-- agent 本地库：持久化事件队列与队列级计数器。本文件一次写完，不再追加修改。
CREATE TABLE event_queue (
    seq        INTEGER PRIMARY KEY,
    id         TEXT NOT NULL UNIQUE,
    kind       TEXT NOT NULL,
    data       TEXT NOT NULL,
    -- at 是事件发生时间（agent 毫秒时间），created_at 是入队时间，7 天淘汰按入队时间算。
    at         INTEGER NOT NULL,
    created_at INTEGER NOT NULL
);
CREATE INDEX event_queue_created_at ON event_queue(created_at);

-- next_seq 记录已分配的最大序号，dropped 记录因淘汰丢弃的累计条数；二者不随队列清空归零。
CREATE TABLE event_counters (
    name  TEXT PRIMARY KEY,
    value INTEGER NOT NULL
);
INSERT INTO event_counters (name, value) VALUES ('next_seq', 0), ('dropped', 0);
