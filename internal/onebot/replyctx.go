package onebot

import (
	"sync"
	"time"
)

const (
	// replyWindow QQ 官方被动回复的有效期
	replyWindow = 5 * time.Minute
	// replyMaxSeq 同一条被动消息最多回复 5 次
	replyMaxSeq = 5
)

type replyEntry struct {
	msgID  string
	expire time.Time
	seq    int
}

// replyCtx 按会话记住最近一条入站消息, 用于自动构造被动回复
type replyCtx struct {
	mu sync.Mutex
	m  map[string]*replyEntry
}

func newReplyCtx() *replyCtx {
	return &replyCtx{m: map[string]*replyEntry{}}
}

func replyKey(scene, target string) string {
	return scene + "|" + target
}

// Set 记录一条入站消息, 后续发送会作为它的被动回复
func (r *replyCtx) Set(scene, target, msgID string) {
	if msgID == "" {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.m[replyKey(scene, target)] = &replyEntry{
		msgID:  msgID,
		expire: time.Now().Add(replyWindow),
	}
}

// Next 返回本次发送可用的 msg_id 与 msg_seq
// 过期或序号用尽时返回空 msg_id, 即主动消息
func (r *replyCtx) Next(scene, target string) (msgID string, seq int) {
	k := replyKey(scene, target)
	r.mu.Lock()
	defer r.mu.Unlock()

	e, ok := r.m[k]
	if !ok {
		return "", 0
	}
	if time.Now().After(e.expire) || e.seq >= replyMaxSeq {
		delete(r.m, k)
		return "", 0
	}
	e.seq++
	return e.msgID, e.seq
}
