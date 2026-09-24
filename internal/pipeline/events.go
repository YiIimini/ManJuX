// Package pipeline 编排五阶段管线:script → assets → render → qc → assemble。
// 每个阶段可独立运行、独立调参(项目级参数快照见 project.StageParams),
// 运行进度通过事件总线(SSE)实时推送前端。
package pipeline

import (
	"encoding/json"
	"sync"
	"time"
)

// Event 一条管线事件。
type Event struct {
	Seq       int       `json:"seq"` // 全局递增序号(轮询增量拉取用)
	ProjectID string    `json:"project_id"`
	Phase     string    `json:"phase"`    // script/assets/render/qc/assemble/pipeline
	ShotID    int       `json:"shot_id"`  // 0=非镜头级
	Level     string    `json:"level"`    // info/success/warn/error
	Progress  int       `json:"progress"` // 当前阶段进度 0-100
	Message   string    `json:"message"`
	At        time.Time `json:"at"`
}

// Bus 事件总线:广播 + 环形历史(新订阅者先回放历史再接增量)。
type Bus struct {
	mu      sync.Mutex
	subs    map[int]chan Event
	nextSub int
	nextSeq int
	history []Event
	maxHist int
	closed  bool
}

// NewBus 创建事件总线。
func NewBus() *Bus {
	return &Bus{subs: map[int]chan Event{}, maxHist: 500}
}

// Publish 发布事件(同时写入历史)。
func (b *Bus) Publish(ev Event) {
	if ev.At.IsZero() {
		ev.At = time.Now()
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		return
	}
	b.nextSeq++
	ev.Seq = b.nextSeq
	b.history = append(b.history, ev)
	if len(b.history) > b.maxHist {
		b.history = b.history[len(b.history)-b.maxHist:]
	}
	for _, ch := range b.subs {
		select {
		case ch <- ev:
		default: // 慢订阅者丢帧:客户端凭 **seq 跳号** 自己发现(见 store.js 的 pushEvent)
		}
	}
}

// Subscribe 订阅:返回事件通道与回放历史。取消时调用 Unsubscribe。
func (b *Bus) Subscribe() (id int, ch chan Event, replay []Event) {
	b.mu.Lock()
	defer b.mu.Unlock()
	id = b.nextSub
	b.nextSub++
	ch = make(chan Event, 256)
	b.subs[id] = ch
	replay = append([]Event(nil), b.history...)
	return id, ch, replay
}

// Unsubscribe 退订。
func (b *Bus) Unsubscribe(id int) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if ch, ok := b.subs[id]; ok {
		delete(b.subs, id)
		close(ch)
	}
}

// Close 关闭总线(进程退出)。
func (b *Bus) Close() {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		return
	}
	b.closed = true
	for id, ch := range b.subs {
		delete(b.subs, id)
		close(ch)
	}
}

// SnapshotJSON 序列化历史(调试用)。
func (b *Bus) SnapshotJSON() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	raw, _ := json.Marshal(b.history)
	return string(raw)
}

// Recent 返回 seq 大于 after 的历史事件(轮询增量)与最新 seq。
func (b *Bus) Recent(after int) ([]Event, int) {
	b.mu.Lock()
	defer b.mu.Unlock()
	out := []Event{}
	for _, ev := range b.history {
		if ev.Seq > after {
			out = append(out, ev)
		}
	}
	return out, b.nextSeq
}
