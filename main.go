package main

import (
	"github.com/pocketbase/pocketbase"
	"github.com/shynome/err0/try"
	_ "github.com/shynome/go-onebots/db/migrations"
	"github.com/shynome/go-onebots/internal/onebot"
)

var Version = "dev"

func main() {
	app := pocketbase.New()
	app.RootCmd.Version = Version

	onebot.Version = Version
	mgr := onebot.NewManager(app)
	initBots(app, mgr)
	initEventsCleanup(app)

	try.To(app.Start())
}
