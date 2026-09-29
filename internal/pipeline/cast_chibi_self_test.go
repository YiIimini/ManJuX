package pipeline

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/YiIimini/ManJuX/internal/project"
)

// Q 版形象**不许**被拿去和本人的主卡比"像不像同一个人"（2026-09-29 用户：这条告警
// 「这判断结论给我搞笑了」）。原文：
//
//	⚠ 重出后「碑生·Q版（肩上巴掌大的自己）」仍与「碑生（丙十七）」像同一个人
//	  （脸型、眼型、眉形一致，额前碎发和额头印记相同，仅年龄不同。）
//
// 同一个人的 Q 版当然是同一张脸 —— 它是本人内心演出用的派生素材，不是"另一个角色"。
// 此前只排了"比对对象是 Q 版"的情况，没排"**自己**是 Q 版"的情况。
func TestChibiNeverComparesAgainstItsOwnBaseCard(t *testing.T) {
	dir := t.TempDir()
	// 真实形状：主卡 + 它的 Q 版（技能侧 id 是 chibi_ 前缀、名字是「·Q版」后缀）
	plan := &project.Plan{Characters: []project.Character{
		{ID: "char_bingsheng", Name: "碑生（丙十七）", Kind: "human"},
		{ID: "chibi_char_bingsheng", Name: "碑生·Q版（肩上巴掌大的自己）", Kind: "chibi"},
		{ID: "char_shenyan", Name: "沈砚", Kind: "human"},
	}}
	assets := map[string]project.Asset{}
	for _, c := range plan.Characters {
		f := filepath.Join(dir, c.ID+".png")
		if err := os.WriteFile(f, []byte("PNG"), 0o644); err != nil {
			t.Fatal(err)
		}
		assets["char:"+c.ID] = project.Asset{Name: c.Name, File: c.ID + ".png"}
	}

	// ① 自己是 Q 版：比对对象里**不许**出现本人的主卡
	peers := castPeers(dir, plan, assets, "chibi_char_bingsheng")
	for _, p := range peers {
		if p.id == "char_bingsheng" {
			t.Fatalf("Q 版被拿去和本人的主卡比了（会报「像同一个人」）：%+v", peers)
		}
	}
	// ② 反方向：自己的主卡也不许和它的 Q 版比
	peers2 := castPeers(dir, plan, assets, "char_bingsheng")
	for _, p := range peers2 {
		if p.id == "chibi_char_bingsheng" {
			t.Fatalf("主卡被拿去和它的 Q 版比了：%+v", peers2)
		}
	}
	// ③ **Q 版不参与撞脸自检**：自己是 Q 版时比对名单为空（跟本人比必然"像"，
	// 跟别人比也没意义 —— 画法档位不同）。2026-09-29 用户第二次报同一件事之后定的口径。
	if len(peers) != 0 {
		t.Fatalf("Q 版不该参与撞脸自检，实得比对名单 %+v", peers)
	}
	// ④ 别的角色仍要比（这道闸门不能把该比的也排掉）：主卡的名单里要有沈砚、没有它的 Q 版
	foundOther := false
	for _, p := range peers2 {
		if p.id == "char_shenyan" {
			foundOther = true
		}
	}
	if !foundOther {
		t.Fatalf("同剧别的角色应当仍在主卡的比对名单里，实得 %+v", peers2)
	}
	// ④ 还原判据本身
	if got := chibiBaseID("chibi_char_bingsheng"); got != "char_bingsheng" {
		t.Errorf("chibiBaseID 前缀写法还原错：%q", got)
	}
	if got := chibiBaseID("char_bingsheng_chibi"); got != "char_bingsheng" {
		t.Errorf("chibiBaseID 后缀写法还原错：%q", got)
	}
	if got := chibiBaseName("碑生·Q版（肩上巴掌大的自己）"); got != "碑生" {
		t.Errorf("chibiBaseName 还原错：%q", got)
	}
	if got := chibiBaseName("沈砚"); got != "" {
		t.Errorf("不是 Q 版就不该还原出基名：%q", got)
	}
}
