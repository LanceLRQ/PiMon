package daemon

import (
	"encoding/json"
	"log/slog"
	"math"

	"github.com/LanceLRQ/PiMon/src/internal/agentlink"
	"github.com/LanceLRQ/PiMon/src/internal/hub/logging"
	"github.com/LanceLRQ/PiMon/src/pkg/model"
	"github.com/LanceLRQ/PiMon/src/pkg/protocol/agent"
)

const (
	// LogsDefaultLimit 是 logs_get 未指定条数时返回的条数。
	LogsDefaultLimit = 200
	// LogsMaxLimit 是 logs_get 的条数上限。
	LogsMaxLimit = 2000
	// LogsMaxBytes 是 logs 应答中日志条目累计的 JSON 字节上限（低于 8 MiB 单帧上限，留足余量）。
	LogsMaxBytes = 6 << 20
	// logsEnvelope 是 logs 消息外层（type、request_id 等）的预留字节。
	logsEnvelope = 256
)

// sendLogs 回 logs_get：从日志环取条目，超过 LogsMaxBytes 时从最旧的开始丢。
func (d *Daemon) sendLogs(s *agentlink.Session, m *agent.LogsGet) {
	entries := buildLogs(d.cfg.Ring, m.Limit, m.MinLevel)
	if err := s.Send(&agent.Logs{RequestID: m.RequestID, Entries: entries}); err != nil {
		d.log.Warn("发送 logs 应答失败", "err", err)
	}
}

// buildLogs 返回级别不低于 minLevel（空表示不过滤）的最近 limit 条日志（0 取默认，上限 LogsMaxLimit），
// 按时间正序；累计 JSON 超过 LogsMaxBytes 时丢弃最旧的。ring 为空时返回空切片。
func buildLogs(ring *logging.Ring, limit int, minLevel string) []model.LogEntry {
	out := []model.LogEntry{}
	if ring == nil {
		return out
	}
	switch {
	case limit <= 0:
		limit = LogsDefaultLimit
	case limit > LogsMaxLimit:
		limit = LogsMaxLimit
	}
	raw := ring.Entries(parseLevel(minLevel), limit)
	sizes := make([]int, len(raw))
	total := logsEnvelope
	for i, e := range raw {
		le := model.LogEntry{Time: e.Time, Level: levelName(e.Level), Message: e.Message, Attrs: e.Attrs}
		out = append(out, le)
		b, err := json.Marshal(le)
		if err != nil {
			continue
		}
		sizes[i] = len(b) + 1 // 逗号
		total += sizes[i]
	}
	drop := 0
	for total > LogsMaxBytes && drop < len(out) {
		total -= sizes[drop]
		drop++
	}
	return out[drop:]
}

// parseLevel 把 logs_get 的 min_level 转成 slog 级别，空表示不过滤。
func parseLevel(s string) slog.Level {
	switch s {
	case "debug":
		return slog.LevelDebug
	case "info":
		return slog.LevelInfo
	case "warn":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	}
	return slog.Level(math.MinInt32)
}

// levelName 与 hub 系统日志的级别名一致（internal/hub/system）。
func levelName(l slog.Level) string {
	switch {
	case l >= slog.LevelError:
		return "error"
	case l >= slog.LevelWarn:
		return "warn"
	case l >= slog.LevelInfo:
		return "info"
	default:
		return "debug"
	}
}
