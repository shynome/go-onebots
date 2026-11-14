package main

import (
	"github.com/pocketbase/pocketbase/core"
	"github.com/shynome/go-onebots/db"
	"github.com/shynome/go-onebots/internal/bot"
	"github.com/shynome/go-onebots/internal/onebot"
)

func initBots(app core.App, mgr *onebot.Manager) {
	app.OnServe().BindFunc(func(e *core.ServeEvent) error {
		e.Router.GET("/onebot/v11/{botId}/ws", mgr.ServeWS)
		e.Router.GET("/onebot/v11/{botId}/{action}", mgr.ServeHTTP)
		e.Router.POST("/onebot/v11/{botId}/{action}", mgr.ServeHTTP)

		records, err := e.App.FindRecordsByFilter(db.TableQQBots, "enabled = true", "", 0, 0)
		if err != nil {
			return err
		}
		for _, r := range records {
			mgr.Get(r.Id)
			bot.Start(e.App, r)
		}
		return e.Next()
	})

	app.OnRecordUpdateRequest(db.TableQQBots).BindFunc(func(e *core.RecordRequestEvent) error {
		action := e.Record.GetString("do_action")
		if action != "" {
			e.Record.Set("do_action", "")
		}
		if err := e.Next(); err != nil {
			return err
		}
		switch action {
		case "stop":
			bot.Stop(e.Record.Id)
			e.Record.Set("ws_status", "手动停止")
		case "restart":
			mgr.Get(e.Record.Id)
			bot.Start(e.App, e.Record)
		}
		return nil
	})

	app.OnRecordDeleteRequest(db.TableQQBots).BindFunc(func(e *core.RecordRequestEvent) error {
		bot.Stop(e.Record.Id)
		return e.Next()
	})

	// 新建时若启用则直接启动, 不必重启服务
	app.OnRecordAfterCreateSuccess(db.TableQQBots).BindFunc(func(e *core.RecordEvent) error {
		mgr.Get(e.Record.Id)
		if e.Record.GetBool("enabled") {
			bot.Start(e.App, e.Record)
		}
		return e.Next()
	})
}
