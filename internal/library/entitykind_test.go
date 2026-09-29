package library

// 实体类型的落库与**死锁**守卫（2026-09-29）。
//
// 【为什么记类型】类型决定整套渲染方法（定妆照取景 / 多视角方式 / 镜头身份锁），
// 而判定要走一次视觉模型。判一次记下来，后续"补全多视角""重抽"直接用 —— 同一张图每次
// 判出不同类型才是灾难（本仓前科：给一只铜雀做"原地转身"）。
//
// 【为什么还要测死锁】`SetEntityKind` 持有**写锁**时又调了 `Match`（它要拿读锁）——
// 同一 goroutine 重入 sync.RWMutex 是**死锁**：一旦有调用方传空串（"没判出来，不覆盖"），
// 那个 HTTP 请求永不返回，而且整库的写锁被占着 ⇒ 资产阶段整条卡死。
// 这类问题不会报错、只会"卡住"，所以判据得是**超时**。

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestSetEntityKindByIDAndEmptyIsNotADeadlock(t *testing.T) {
	root := t.TempDir()
	src := t.TempDir()
	img := filepath.Join(src, "a.png")
	if err := os.WriteFile(img, tinyPNG, 0o644); err != nil {
		t.Fatal(err)
	}
	l, err := New(root)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := l.PutCard("characters", "char_qiqi", "祁七", "外观二十岁", "PROMPT", img, "p1", "qwen-image-2.1"); err != nil {
		t.Fatal(err)
	}

	type result struct {
		ek  string
		err error
	}
	call := func(name, ek string) (string, error) {
		ch := make(chan result, 1)
		go func() {
			e, err := l.SetEntityKind("characters", name, ek)
			if e == nil {
				ch <- result{"", err}
				return
			}
			ch <- result{e.EntityKind, err}
		}()
		select {
		case r := <-ch:
			return r.ek, r.err
		case <-time.After(3 * time.Second):
			t.Fatal("SetEntityKind 卡住了 —— 十有八九是持写锁时又去拿读锁（重入 RWMutex = 死锁）；" +
				"它占着整库的写锁，资产阶段会整条卡死")
			return "", nil
		}
	}

	// ① 按**卡片 id** 落类型（界面与资产阶段都是这么传的）
	if ek, err := call("char_qiqi", "incorporeal"); err != nil || ek != "incorporeal" {
		t.Fatalf("按 id 记类型失败：ek=%q err=%v（定位必须 id 优先）", ek, err)
	}
	// ② 空值 = "没判出来，别覆盖已有判断"，且**不能死锁**
	ek, err := call("char_qiqi", "")
	if err != nil {
		t.Fatalf("空值调用应安静返回：%v", err)
	}
	if ek != "incorporeal" {
		t.Fatalf("空值不许抹掉已知类型，实得 %q", ek)
	}
	// ③ 落库的类型要能被**名字**与**id**两条路读到（补多视角/重抽都靠它）
	if e, _, ok := l.Match("characters", "char_qiqi"); !ok || e.EntityKind != "incorporeal" {
		t.Fatalf("按 id 读不到刚记的类型：%+v", e)
	}
	if e, _, ok := l.Match("characters", "祁七"); !ok || e.EntityKind != "incorporeal" {
		t.Fatalf("按名字读不到刚记的类型：%+v", e)
	}
}
