package bot

import (
	"database/sql"
	"errors"
	"sync"

	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/core"
	"github.com/shynome/go-onebots/db"
)

// wsStateStore 把网关 session 与 seq 存进 PocketBase,
// 让进程重启后仍能 resume, 拿到断连期间的事件
type wsStateStore struct {
	app   core.App
	botID string

	mu  sync.Mutex
	rec *core.Record
}

func newWSStateStore(app core.App, botID string) *wsStateStore {
	return &wsStateStore{app: app, botID: botID}
}

func (s *wsStateStore) LoadSession() (string, uint64) {
	s.mu.Lock()
	defer s.mu.Unlock()

	rec, err := s.recordLocked()
	if err != nil || rec == nil {
		return "", 0
	}
	return rec.GetString("session_id"), uint64(rec.GetFloat("seq"))
}

func (s *wsStateStore) SaveSession(sessionID string, seq uint64) {
	s.mu.Lock()
	defer s.mu.Unlock()

	rec, err := s.recordLocked()
	if err != nil || rec == nil {
		s.app.Logger().Warn("保存网关会话失败", "bot", s.botID, "error", err)
		return
	}
	if rec.GetString("session_id") == sessionID && uint64(rec.GetFloat("seq")) == seq {
		return
	}

	rec.Set("session_id", sessionID)
	rec.Set("seq", seq)
	if err := s.app.Save(rec); err != nil {
		s.app.Logger().Warn("保存网关会话失败", "bot", s.botID, "error", err)
	}
}

// recordLocked 调用方必须持有 s.mu
func (s *wsStateStore) recordLocked() (*core.Record, error) {
	if s.rec != nil {
		return s.rec, nil
	}

	rec, err := s.app.FindFirstRecordByFilter(db.TableWSState, "bot = {:bot}",
		dbx.Params{"bot": s.botID})
	if err == nil {
		s.rec = rec
		return rec, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}

	col, err := s.app.FindCachedCollectionByNameOrId(db.TableWSState)
	if err != nil {
		return nil, err
	}
	rec = core.NewRecord(col)
	rec.Set("bot", s.botID)
	s.rec = rec
	return rec, nil
}
