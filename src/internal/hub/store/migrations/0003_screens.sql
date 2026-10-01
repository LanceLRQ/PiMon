-- M1d：屏幕与布局相关表。本文件一次写完（含后续子任务才写逻辑的表），迁移提交后不可再改。

-- 布局版本：每次保存、回滚都新增一行，只保留最近 20 个版本（由 screens 包清理）。
-- 当前布局就是最大版本号那一行，没有独立的 screens 表，避免出现两个事实源。
-- layout_json 是整份布局（网格加全部 screen）；summary 是版本摘要 JSON；
-- source 取值 seed | edit | rollback | auto，由代码校验。
CREATE TABLE layout_versions (
    version     INTEGER PRIMARY KEY,
    layout_json TEXT NOT NULL,
    summary     TEXT NOT NULL DEFAULT '{}',
    source      TEXT NOT NULL,
    created_at  TEXT NOT NULL
);

-- 屏幕时段计划：单行，整份计划为一个 JSON 文档（结构由 screenstate 包定义）。
CREATE TABLE schedule (
    id            INTEGER PRIMARY KEY CHECK (id = 1),
    schedule_json TEXT NOT NULL,
    updated_at    TEXT NOT NULL
);

-- 屏幕远程操作记录：action 为 refresh | switch | on | off | wake | token_reset 等，
-- params_json 是操作参数，client_ip 是发起者来源，delivered 表示一次性指令是否已送达屏幕。
CREATE TABLE screen_ops (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    action      TEXT NOT NULL,
    params_json TEXT NOT NULL DEFAULT '{}',
    client_ip   TEXT NOT NULL DEFAULT '',
    delivered   INTEGER NOT NULL DEFAULT 0 CHECK (delivered IN (0, 1)),
    at          TEXT NOT NULL
);
CREATE INDEX idx_screen_ops_at ON screen_ops (at);

-- 设置码加密副本：让 hub 重启后仍能向管理员显示有效的设置码；空串表示不可显示。
ALTER TABLE setup_codes ADD COLUMN code_enc TEXT NOT NULL DEFAULT '';
