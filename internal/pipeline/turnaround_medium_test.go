package pipeline

import (
	"strings"
	"testing"
)

// 转身视频的**介质锁**回归(2026-09-24 用户报「角色多视角老是生成错误」):
//
// 症状:三个角色的多视角连续失败,逐帧身份校验报"与定妆照不是同一主体"。
// 自查抽帧(定妆照 + 1.3s/2.6s/3.9s 三帧拼图)看得很清楚 —— 帧**真的画错了**:
//   - 赛璐璐动漫角色 → 被重画成写实真人(素色和服、迷彩纹样丢失);
//   - 插画风青铜雀   → 被重画成写实 3D 机械铜鸟(铆钉/玻璃眼/金属板)。
//
// 判定没错,缺的是介质锁:TurnaroundPrompt 从来没带过它,而转身视频是**另一次
// 渲染调用**,拿不到分镜那套 styleAndMediumBlock,于是 H3 用自己的默认介质重画。
//
// 这个测试钉住:① 非写实项目必须有介质锁;② 器物不得被塞人类专属的脸部条款;
// ③ 用户显式要真人剧时**不**加锁(强塞动画介质同样是错的);④ 提示词里确实带上了它。
func TestTurnaroundMediumLock(t *testing.T) {
	// ① 人:通用介质 + 人脸条款
	human := TurnaroundMediumBlock(KindHuman, false)
	for _, want := range []string{RenderMediumMarker, "cinematic photoreal 3D render", "idealised and semi-stylised"} {
		if !strings.Contains(human, want) {
			t.Errorf("人类转身介质锁缺少 %q:\n%s", want, human)
		}
	}

	// ② 器物/妖兽/灵体:通用介质,且**不含**人类专属的**正面**脸部条款
	//(给一件器物写"手绘线稿脸/哑光肤色"就是在逼它长一张脸 —— 阿凿铜雀事故的同款)。
	// 注意:通用块里出现 "human face" 是**排除句**(不许长成被拍摄的真人脸),
	// 对器物同样成立,所以判据不能是"含 face 字样",而是"没有人类的正面脸部条款"。
	for _, k := range []EntityKind{KindObject, KindSpiritPet, KindBeast, KindIncorporeal} {
		got := TurnaroundMediumBlock(k, false)
		if !strings.Contains(got, RenderMediumMarker) {
			t.Errorf("%s 缺少通用介质块:\n%s", k, got)
		}
		for _, banned := range []string{"Every human face is idealised and semi-stylised", "Skin specifically"} {
			if strings.Contains(got, banned) {
				t.Errorf("%s 的介质锁混进了人类专属条款 %q(会逼非人实体长脸)", k, banned)
			}
		}
	}
	// 人类反过来必须带这两条;Q 版同样有脸,走同一支
	for _, k := range []EntityKind{KindHuman, KindChibi} {
		got := TurnaroundMediumBlock(k, false)
		for _, want := range []string{"Every human face is idealised and semi-stylised", "Skin specifically"} {
			if !strings.Contains(got, want) {
				t.Errorf("%s 介质锁缺少人类专属条款 %q", k, want)
			}
		}
	}

	// ③ 显式要真人写实:不加锁
	if got := TurnaroundMediumBlock(KindHuman, true); got != "" {
		t.Errorf("realistic=true 时不该加动画介质锁,实得:\n%s", got)
	}

	// ④ 提示词里确实带上了锁,并且点明了"介质是身份的一部分"
	p := EntityProfileOf(KindHuman)
	withLock := p.TurnaroundPrompt("鲁青砚", "a young craftsman", TurnaroundMediumBlock(KindHuman, false))
	for _, want := range []string{
		"MEDIUM LOCK (highest priority)",
		"MEDIUM IS PART OF THE IDENTITY",
		RenderMediumMarker,
		"RENDERING MEDIUM IS PART OF THE IDENTITY",
		"a photographic or 3D-rendered version of the subject",
	} {
		if !strings.Contains(withLock, want) {
			t.Errorf("转身提示词缺少 %q", want)
		}
	}
	// 旧行为(不传锁)必须仍然能构造出提示词,且没有锁的字样 —— 保证调用方漏传时
	// 是"回到旧行为"而不是报错(但那时多视角会照旧失败,所以调用方必须传)。
	noLock := p.TurnaroundPrompt("鲁青砚", "a young craftsman", "")
	if strings.Contains(noLock, "MEDIUM LOCK") {
		t.Error("空介质锁不该凭空生成锁段落")
	}
	// 身份句仍然在(别为了加锁把主体定义顶掉)
	if !strings.Contains(withLock, "subject_definitions:") || !strings.Contains(withLock, "retention_analysis:") {
		t.Error("六段式骨架被破坏")
	}
}

// TestRealisticForPrompt 没有项目上下文时(资产库「补全多视角」)靠定妆照提示词
// 反推介质线:带我们的介质标记 = 动画线,必须锁;上传的写实图 = 不锁。
func TestRealisticForPrompt(t *testing.T) {
	if RealisticForPrompt("a young craftsman" + MediumMarker + " (mandatory): ...") {
		t.Error("带介质标记的定妆照应判为动画线(不该当作写实)")
	}
	if !RealisticForPrompt("a photo of a young craftsman, 35mm film grain") {
		t.Error("没有介质标记(多半是上传的写实图)应判为写实线")
	}
	if !RealisticForPrompt("") {
		t.Error("空提示词应判为写实线(不擅自动画化)")
	}
}

// 标记串是两处判据(已经带过就不再叠 / 反推介质线),文案与常量必须同步。
func TestMediumMarkerMatchesBlock(t *testing.T) {
	if !strings.Contains(animatedMediumImageBlock(), MediumMarker) {
		t.Fatalf("介质块里找不到标记串 %q", MediumMarker)
	}
	if !strings.Contains(animatedMediumRenderBlock(), RenderMediumMarker) {
		t.Fatal("H3 侧介质块的标题串变了,标注/判定要一起改")
	}
}
