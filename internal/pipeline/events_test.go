package pipeline

import (
	"testing"
	"time"
)

// 事件总线"序号语义"契约(审计 P2 的缺口判据)。
//
// 起因:慢订阅者丢帧(通道满即丢,见 Publish 的 default 分支)与环形历史截断
// (500 条)都会让客户端**静默**少看到一段日志 —— 日志面板看起来连续,用户以为
// "就这些"。修复的核心不是让总线不丢(那会把慢客户端拖死),而是让**序号连续**,
// 于是客户端能用"跳号"自己发现缺口(前端 pushEvent 负责显示)。
//
// 这三条语义一旦被改坏,缺口判据就会失效(表现为"永远不报缺"或"每次都误报")。
func TestEventSeqSemantics(t *testing.T) {
	b := NewBus()
	defer b.Close()

	// ① 序号从 1 开始、严格递增(客户端靠 +1 判连续)
	for i := 1; i <= 5; i++ {
		b.Publish(Event{Message: "m"})
	}
	evs, latest := b.Recent(0)
	if len(evs) != 5 {
		t.Fatalf("应有 5 条历史,实际 %d", len(evs))
	}
	for i, ev := range evs {
		if ev.Seq != i+1 {
			t.Errorf("第 %d 条的 seq 应为 %d,实际 %d", i+1, i+1, ev.Seq)
		}
	}
	if latest != 5 {
		t.Errorf("最新 seq 应为 5,实际 %d", latest)
	}

	// ② 增量拉取:after=N 只返回 N 之后的,且**不跳号**
	evs, latest = b.Recent(3)
	if len(evs) != 2 || evs[0].Seq != 4 || evs[1].Seq != 5 {
		t.Fatalf("after=3 应返回 seq 4/5,实际 %+v(latest=%d)", evs, latest)
	}
	if latest != 5 {
		t.Errorf("latest 必须回传,客户端据此判断自己是否落后:实际 %d", latest)
	}

	// ③ 环形截断:超出 maxHist 后最早的那些**消失**(客户端正是靠跳号发现这件事)
	over := b.maxHist + 10
	for i := 0; i < over; i++ {
		b.Publish(Event{Message: "x"})
	}
	evs, latest = b.Recent(0)
	if len(evs) != b.maxHist {
		t.Fatalf("历史应被截到 %d 条,实际 %d", b.maxHist, len(evs))
	}
	if evs[0].Seq != latest-b.maxHist+1 {
		t.Errorf("被截断后最早一条应是 latest-%d+1,实际 %d(latest=%d)", b.maxHist, evs[0].Seq, latest)
	}
	if got := b.maxHist; got != 500 {
		t.Errorf("环形历史容量是客户端缺口提示的前提(README/架构文档写的 500),实际 %d", got)
	}
}

// TestEventTimeIsStamped 时间戳由总线补:客户端按 at 排序/展示,空值会让日志面板
// 显示成 1970 年(有过这类 bug 的观感问题)。
func TestEventTimeIsStamped(t *testing.T) {
	b := NewBus()
	defer b.Close()
	b.Publish(Event{Message: "no time"})
	evs, _ := b.Recent(0)
	if len(evs) != 1 || evs[0].At.IsZero() {
		t.Fatal("总线必须给未填时间的事件补上时间戳")
	}
	if time.Since(evs[0].At) > time.Minute {
		t.Errorf("时间戳应是「现在」,实际 %v", evs[0].At)
	}
}
