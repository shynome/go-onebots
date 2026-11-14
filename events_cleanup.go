package main

import (
	"log/slog"
	"time"

	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/core"
	"github.com/shynome/go-onebots/db"
	"github.com/shynome/go-onebots/internal/media"
)

const (
	// eventsCleanupCron 每天 03:30 清理一次
	eventsCleanupCron = "30 3 * * *"
)

// eventsRetentionDays 跟随 PocketBase 的 logs.maxDays
// 返回 0 表示不清理(与 PocketBase 的语义一致)
func eventsRetentionDays(app core.App) int {
	return app.Settings().Logs.MaxDays
}

// initEventsCleanup 定时清理过期事件, 保留天数与 PocketBase logs 一致
func initEventsCleanup(app core.App) {
	cleanup := func() {
		days := eventsRetentionDays(app)
		if days <= 0 {
			slog.Info("事件表清理已关闭", "retention_days", days)
			return
		}

		cutoff := time.Now().AddDate(0, 0, -days).UTC().Format("2006-01-02 15:04:05.000Z")
		res, err := app.DB().
			NewQuery("DELETE FROM " + db.TableEvents + " WHERE created < {:cutoff}").
			Bind(dbx.Params{"cutoff": cutoff}).
			Execute()
		if err != nil {
			slog.Error("清理事件表失败", "error", err)
		} else if affected, _ := res.RowsAffected(); affected > 0 {
			slog.Info("已清理过期事件", "deleted", affected, "retention_days", days)
		}

		cleanupMedia(app, days)
	}

	if err := app.Cron().Add("cleanup_onebot_events", eventsCleanupCron, cleanup); err != nil {
		slog.Error("注册事件清理任务失败", "error", err)
	}
}

// cleanupMedia 清理过期中转媒体, 关联文件由 PocketBase 自动删除
func cleanupMedia(app core.App, days int) {
	store := media.New(app)
	cutoff := time.Now().AddDate(0, 0, -days)
	deleted, err := store.Cleanup(cutoff, 200)
	if err != nil {
		slog.Error("清理中转媒体失败", "error", err, "deleted", deleted)
		return
	}
	if deleted > 0 {
		slog.Info("已清理过期媒体", "deleted", deleted, "retention_days", days)
	}
}
