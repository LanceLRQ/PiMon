package hubself

import (
	"time"

	"github.com/LanceLRQ/PiMon/src/pkg/plugin/report"
)

// 「异地备份」数据项。
const (
	keyRemoteBackup = "hub.remote_backup"

	// remoteBackupMaxAge 是最近一次成功上传的最长允许间隔，超过即 warning。
	remoteBackupMaxAge = 48 * time.Hour

	// 状态文本同时是 i18n 键，前端按 plugin.hub-self.<文本> 翻译。
	textBackupOK     = "backup_ok"
	textBackupFailed = "backup_failed"
	textBackupStale  = "backup_stale"
	textBackupNever  = "backup_never"
)

// RemoteBackupStatus 是远端备份的状态摘要，时间取上传结束时间。
type RemoteBackupStatus struct {
	Enabled     bool
	LastAttempt *time.Time
	LastSuccess *time.Time
	// LastFailed 表示最近一次上传尝试失败。
	LastFailed bool
}

// RemoteBackupStats 是可选接口：Stats 的实现同时实现它时，hub-self 在远端备份启用后输出 hub.remote_backup。
type RemoteBackupStats interface {
	// RemoteBackup 返回远端备份状态；读取失败时返回错误（数据项显示为未知）。
	RemoteBackup() (RemoteBackupStatus, error)
}

// remoteBackupItem 按「最近一次尝试失败 → critical；否则最近成功不超过 48 小时（整点算）→ ok；
// 否则（从未成功或已超过 48 小时）→ warning」判定。未启用或来源未接入时不输出（ok 为 false）。
func remoteBackupItem(st Stats, now time.Time) (report.Item, bool) {
	rs, ok := st.(RemoteBackupStats)
	if !ok {
		return report.Item{}, false
	}
	s, err := rs.RemoteBackup()
	if err != nil {
		return report.Item{Key: keyRemoteBackup, Type: report.TypeState, State: report.StatusUnknown, Text: "unknown",
			Error: "无法读取异地备份状态 / Cannot read remote backup status"}, true
	}
	if !s.Enabled {
		return report.Item{}, false
	}
	it := report.Item{Key: keyRemoteBackup, Type: report.TypeState}
	switch {
	case s.LastFailed:
		it.State, it.Text = report.StatusCritical, textBackupFailed
	case s.LastSuccess != nil && !now.After(s.LastSuccess.Add(remoteBackupMaxAge)):
		it.State, it.Text = report.StatusOK, textBackupOK
	case s.LastSuccess != nil:
		it.State, it.Text = report.StatusWarning, textBackupStale
	default:
		it.State, it.Text = report.StatusWarning, textBackupNever
	}
	return it, true
}
