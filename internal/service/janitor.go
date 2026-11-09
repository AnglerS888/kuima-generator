package service

import (
	"time"

	"wxcode/internal/logger"
	"wxcode/internal/store"
)

const cleanupInterval = 6 * time.Hour

const RetentionMaxDays = 3650

// startJanitor 启动后台清理任务，返回一个用于停止的函数。
//
// 立刻先跑一次（把上次退出期间攒下的过期数据清掉），之后按固定间隔跑。
func StartJanitor(stop chan struct{}) {
	// 启动时先清一次
	CleanupExpired()

	go func() {
		ticker := time.NewTicker(cleanupInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				CleanupExpired()
			case <-stop:
				return
			}
		}
	}()
}

// cleanupExpired 清掉过期会话与过期记录。
//
// 清理失败只记 warn 不中断：清理是尽力而为的事，
// 但失败必须留下痕迹 —— 否则磁盘悄悄涨满时没人知道。
func CleanupExpired() {
	// 会话：过期即无意义，随时可清
	if _, err := store.DB.Exec(`DELETE FROM sessions WHERE expires_at < ?`, store.NowStr()); err != nil {
		logger.Warn("清理过期会话失败", "error", err.Error())
	}

	// 邮箱验证码：过期/被放弃的记录没有保留价值（发信与校验成功时会即时删除）
	if _, err := store.DB.Exec(`DELETE FROM email_codes WHERE expires_at < ?`, store.NowStr()); err != nil {
		logger.Warn("清理过期邮箱验证码失败", "error", err.Error())
	}

	// 记录：按配置的保留天数
	days := RecordRetentionDays()
	if days <= 0 {
		return
	}
	cutoff := time.Now().UTC().AddDate(0, 0, -days).Format(time.RFC3339)
	res, err := store.DB.Exec(`DELETE FROM records WHERE created_at < ?`, cutoff)
	if err != nil {
		logger.Warn("清理过期记录失败", "error", err.Error(), "retention_days", days)
		return
	}
	if n, _ := res.RowsAffected(); n > 0 {
		logger.Info("已清理过期记录", "count", n, "retention_days", days)
	}
}

// recordRetentionDays 返回记录保留天数（0 = 永久保留）。
func RecordRetentionDays() int {
	d := store.SettingInt(store.SetRecordRetentionDays, store.RetentionDefault)
	if d < 0 {
		return 0
	}
	if d > RetentionMaxDays {
		return RetentionMaxDays
	}
	return d
}

// recordRetentionSummary 给后台显示用：保留天数 + 当前记录条数 + 最早一条的时间。
func RecordRetentionSummary() map[string]any {
	days := RecordRetentionDays()
	out := map[string]any{
		"retentionDays": days,
	}
	var total int
	_ = store.DB.QueryRow(`SELECT COUNT(*) FROM records`).Scan(&total)
	out["total"] = total
	var oldest string
	_ = store.DB.QueryRow(`SELECT created_at FROM records ORDER BY id ASC LIMIT 1`).Scan(&oldest)
	out["oldest"] = oldest
	return out
}
