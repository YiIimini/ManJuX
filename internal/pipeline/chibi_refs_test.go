package pipeline

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/YiIimini/ManJuX/internal/project"
)

// 生产形态的资产表（真机《被开除一年后，我回公司送水·第01集》原样）：
// Q 版卡的 **id 是 `char_jianning_q`**、**名字是「简宁·Q版」** —— 两条互不相等，
// 而模型在 `chibi_ids` 里写的是**名字**。此前渲染侧的顶替映射只认 `chibi_<本体id>`
// 且去找资产键 `char:chibi_<本体id>`（生产上不存在这个键）⇒ 顶替从来没生效过：
// 内心镜附的是**本人定妆照**（口径明令禁止"本人演这段"）。
func prodChibiAssets() map[string]project.Asset {
	return map[string]project.Asset{
		"char:char_jianning": {
			ID: "char:char_jianning", Kind: "characters", Name: "简宁",
			File: "assets/characters/char_jianning.png",
		},
		"char:char_jianning_q": {
			ID: "char:char_jianning_q", Kind: "characters", Name: "简宁·Q版",
			File: "assets/characters/char_jianning_q.png",
		},
	}
}

// 内心镜：`chibi_ids` 写**显示名**时也必须附上 Q 版图、且**不附本人照**。
func TestInnerShotSubstitutesChibiFromDisplayName(t *testing.T) {
	cases := []struct {
		name string
		ref  string
	}{
		{"真机原样：显示名", "简宁·Q版"},
		{"技能侧前缀写法", "chibi_char_jianning"},
		{"平台派生后缀写法", "char_jianning_chibi"},
		{"库卡 id（装配点归一后的样子）", "char_jianning_q"},
	}
	for _, c := range cases {
		dir := t.TempDir()
		assets := prodChibiAssets()
		mkAssetsOnDisk(t, dir, assets)
		// 真机 #8/#15 原样：出场名单里是**本人** char_jianning，chibi_ids 指 Q 版。
		shot := project.Shot{
			VoKind:     project.VoKindInner,
			Characters: []string{"char_jianning"},
			ChibiIDs:   []string{c.ref},
		}
		refs, roles, missing, warnings := RefPlan(dir, shot, assets, true)
		if len(refs) != 1 || len(roles) != 1 {
			t.Fatalf("%s：该只附 1 张图（Q 版），实际 %d 张（missing=%v warn=%v）",
				c.name, len(refs), missing, warnings)
		}
		if !strings.HasSuffix(refs[0].Path, "char_jianning_q.png") {
			t.Errorf("%s：内心镜附的是 %s —— 口径要求由 Q 版演出、**本人不演这段**",
				c.name, refs[0].Path)
		}
		if roles[0].Kind != "character_chibi" {
			t.Errorf("%s：参考图角色该是 character_chibi，实际 %q（REFERENCE ORDER 契约按它说明用途）",
				c.name, roles[0].Kind)
		}
		if len(missing) != 0 || len(warnings) != 0 {
			t.Errorf("%s：不该有缺失/告警（missing=%v warn=%v）", c.name, missing, warnings)
		}
	}
}

// 真机镜 15 的形态：出场名单里**本人与 Q 版卡都写着**，chibi_ids 又是显示名。
// 判据：Q 版图只占一个参考图位（同一张图不重复附），且**本人照不附**。
func TestInnerShotChibiListedAsCharacter(t *testing.T) {
	dir := t.TempDir()
	assets := prodChibiAssets()
	mkAssetsOnDisk(t, dir, assets)
	shot := project.Shot{
		VoKind:     project.VoKindInner,
		Characters: []string{"char_jianning", "char_jianning_q"},
		ChibiIDs:   []string{"简宁·Q版"},
	}
	refs, roles, _, _ := RefPlan(dir, shot, assets, true)
	if len(refs) != 1 {
		var got []string
		for _, r := range refs {
			got = append(got, filepath.Base(r.Path))
		}
		t.Fatalf("同一张 Q 版图只该占一个参考图位，实际 %d 张：%v", len(refs), got)
	}
	if roles[0].Kind != "character_chibi" {
		t.Errorf("该按 Q 版挂（character_chibi），实际 %q", roles[0].Kind)
	}
}

// 认不出来就**不猜**：写成本人名（`简宁`）不是 Q 版写法，按原样附本人照，
// 并把"这一镜本该由 Q 版演"交给契约门去点名（门对同一份引用报悬空）。
func TestUnknownChibiRefDoesNotGuess(t *testing.T) {
	dir := t.TempDir()
	assets := prodChibiAssets()
	mkAssetsOnDisk(t, dir, assets)
	shot := project.Shot{
		VoKind:     project.VoKindInner,
		Characters: []string{"char_jianning"},
		ChibiIDs:   []string{"简宁"},
	}
	refs, _, _, _ := RefPlan(dir, shot, assets, true)
	if len(refs) != 1 || !strings.HasSuffix(refs[0].Path, "char_jianning.png") {
		t.Errorf("认不出来的引用不该被猜成某张 Q 版卡（实际 %v）", refs)
	}
}

// 非内心镜一个字不许变：`chibi_ids` 只在内心镜参与顶替。
func TestChibiSubstitutionOnlyForInnerShots(t *testing.T) {
	dir := t.TempDir()
	assets := prodChibiAssets()
	mkAssetsOnDisk(t, dir, assets)
	shot := project.Shot{
		VoKind:     "speech",
		Characters: []string{"char_jianning"},
		ChibiIDs:   []string{"简宁·Q版"},
	}
	refs, _, _, _ := RefPlan(dir, shot, assets, true)
	if len(refs) != 1 || !strings.HasSuffix(refs[0].Path, "char_jianning.png") {
		t.Errorf("非内心镜不该被 Q 版顶替（实际 %v）", refs)
	}
}
