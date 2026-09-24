package pipeline

import (
	"strings"
	"testing"
)

// 图像编辑式多视角的**提示词契约**测试。
//
// 编辑式模型吃的是"对 image 1 做什么"。指令少说一句,模型就多一次自由发挥的机会,
// 而它的自由发挥全部会落在**拼进转台图的那张图**上,再作为角色的第二张参考喂进渲染。
// 所以这里逐条锁住四件必须说清的事:转多少度、主体不变、介质不变、构图/背景/光线不变。

// TestViewAnglesMatchVideoPathTags 三个角度的 tag 必须与 H3 路径的抽帧 tag 逐字一致。
//
// 为什么是硬约束:tag 同时决定 ①文件名(<base>_view_<tag>.png,资产库靠 glob 收集)
// 与 ②前端 LibraryView 的 labelOf() 标签(threequarter/side/back → 四分之三/侧面/背面)。
// 一旦写错,产物既收不进库、也显示不出角度,而且不会报任何错。
func TestViewAnglesMatchVideoPathTags(t *testing.T) {
	want := []string{"threequarter", "side", "back"}
	if len(viewAngles) != len(want) {
		t.Fatalf("应有 %d 个角度,实际 %d", len(want), len(viewAngles))
	}
	for i, tag := range want {
		if viewAngles[i].Tag != tag {
			t.Errorf("第 %d 个角度的 tag 应为 %q,实际 %q", i+1, tag, viewAngles[i].Tag)
		}
	}
}

// TestViewEditPromptStatesAngle 每个角度都要说清"转多少度",且必须点名"仍看得见脸"之类的
// 可验证特征 —— 只写 "different angle" 时模型倾向于给一张几乎没转的图(等于浪费一次生成)。
func TestViewEditPromptStatesAngle(t *testing.T) {
	for _, a := range viewAngles {
		p := viewEditPrompt(KindHuman, a, "鲁青砚", "a Chinese young man in grey robes", false)
		if !strings.Contains(p, a.En) {
			t.Errorf("%s:提示词里应含该角度的指令原文", a.Tag)
		}
		if !strings.Contains(p, "image 1") {
			t.Errorf("%s:编辑式模型必须点名 image 1", a.Tag)
		}
	}
}

// TestViewEditPromptLocksSubjectAndMedium 身份与介质两条锁都不能丢。
//
// 身份锁:编辑模型最常见的失败不是"画风变了",而是"顺手把人美颜/年轻化了一遍" ——
// 这类偏移单看一张图很像,拼进转台图与定妆照并排就露馅。
func TestViewEditPromptLocksSubjectAndMedium(t *testing.T) {
	p := viewEditPrompt(KindHuman, viewAngles[1], "鲁青砚", "a Chinese young man", false)
	for _, want := range []string{
		"EXACT SAME subject", // 身份锁
		"NOT a redesign",     // 不是重画
		"not a beautified",   // 不许美颜/风格化/改年龄
		MediumMarker,         // 介质锁(与定妆照提示词同一段词)
		"SAME plain background",
		"SAME lighting",
	} {
		if !strings.Contains(p, want) {
			t.Errorf("编辑指令缺 %q:\n%s", want, p)
		}
	}
}

// TestViewEditPromptMediumRespectsRealistic 用户显式要真人写实(关掉动漫化)时,
// **不得**再强塞动画介质锁 —— 那种情况下它才是错的(与 H3 路径同一判据)。
func TestViewEditPromptMediumRespectsRealistic(t *testing.T) {
	p := viewEditPrompt(KindHuman, viewAngles[0], "老王", "a Chinese man", true)
	if strings.Contains(p, MediumMarker) {
		t.Errorf("realistic 模式不应带动画介质锁:\n%s", p)
	}
	if !strings.Contains(p, "EXACT SAME rendering medium") {
		t.Error(`即使 realistic,也仍要锁住「与 image 1 同介质」(不能这边要写实、那边被重画成插画)`)
	}
}

// TestViewEditMediumBlockPerKind 介质块的选词按实体类型分流,且用的是**图像侧**那套
// (与定妆照提示词同一段词),不是视频侧那套(那套写的是 "every frame of this video")。
func TestViewEditMediumBlockPerKind(t *testing.T) {
	human := ViewEditMediumBlock(KindHuman, false)
	if !strings.Contains(human, MediumMarker) {
		t.Errorf("人/物 的介质块应含标记 %q,实际 %q", MediumMarker, human)
	}
	if strings.Contains(human, "frame") {
		t.Errorf("图像侧介质块不该出现视频用语(frame):%q", human)
	}
	if obj := ViewEditMediumBlock(KindObject, false); obj == "" || obj == human {
		t.Error("器物应走图像侧的通用介质块,而不是人脸那一套")
	}
	if got := ViewEditMediumBlock(KindHuman, true); got != "" {
		t.Errorf("realistic 时介质块应为空,实际 %q", got)
	}
}

// TestViewEditPromptPerKindSubject 实体类型决定"要保住什么":
// 器物是结构/材质/纹样"不许增删改",灵宠/妖兽是物种与体表,无实体是轮廓/辉光/透明度。
// 漏判类型会让一只铜雀按人形条款去保"脸和发型" —— 等于没锁。
func TestViewEditPromptPerKindSubject(t *testing.T) {
	cases := []struct {
		kind EntityKind
		want string
	}{
		{KindObject, "no part may be added, removed or redesigned"},
		{KindSpiritPet, "same species, body plan"},
		{KindBeast, "same species, body plan"},
		{KindIncorporeal, "bodiless entity"},
	}
	for _, c := range cases {
		p := viewEditPrompt(c.kind, viewAngles[0], "铜雀", "an ornate bronze sparrow", false)
		if !strings.Contains(p, c.want) {
			t.Errorf("%s 的编辑指令应含 %q:\n%s", c.kind, c.want, p)
		}
	}
}

// TestViewEditPromptIdentityHintIsStripped 提示词里的身份补充必须经过 compactIdentity
// 剥掉面部/皮肤类形容词 —— 长相只认参考图,文字多写一句就多一次"按文字重画"的机会
// (H3 路径上实测过:腮红/红鼻头就是这么来的)。构图形容也要剥(它与"换个角度"直接打架)。
//
// 判据只看**身份补充那一段**:提示词主体本来就有 "SAME plain background"/"framing"
// 这类构图条款(那是要求"保持不变",与"剥掉构图指令"不矛盾),整串搜会误报。
func TestViewEditPromptIdentityHintIsStripped(t *testing.T) {
	p := viewEditPrompt(KindHuman, viewAngles[0], "鲁青砚",
		"a Chinese young man with rosy cheeks and a long chin, front-facing, plain background, upper-body framing", false)
	const hintPrefix = "(The subject is: "
	i := strings.Index(p, hintPrefix)
	if i < 0 {
		t.Fatalf("提示词应带身份补充段:\n%s", p)
	}
	hint := strings.ToLower(strings.TrimRight(p[i+len(hintPrefix):], ")"))
	// 整词判据:不能拿子串搜 —— "chinese" 里就有 "chin",那是必须保留的族群锚点。
	for _, bad := range []string{"rosy", "cheeks", "chin", "front-facing", "upper-body", "plain background"} {
		for _, w := range strings.FieldsFunc(hint, func(r rune) bool { return !isWordRune(r) }) {
			if w == bad {
				t.Errorf("身份补充里不该出现整词 %q(应按 compactIdentity 剥掉):%q", bad, hint)
			}
		}
		if strings.Contains(hint, bad) && (bad == "front-facing" || bad == "plain background" || bad == "upper-body") {
			t.Errorf("身份补充里不该出现 %q:%q", bad, hint)
		}
	}
	// 族群信息是身份锚,必须保留(剥太狠会把角色画成外国人)。
	if !strings.Contains(hint, "chinese") {
		t.Errorf("族群信息应保留:%q", hint)
	}
	// 句尾不许留孤儿词(见 tidyCompact 的说明:悬空的 "long" 会被模型接到别处)。
	for _, bad := range []string{"with", "and", "long"} {
		if strings.HasSuffix(strings.TrimSpace(hint), " "+bad) {
			t.Errorf("身份补充末尾不该留下孤儿词 %q:%q", bad, hint)
		}
	}
}

// isWordRune 与 compactIdentity 的\"整词\"判据同源(字母/数字/下划线算词内字符)。
func isWordRune(r rune) bool {
	return r == '_' || (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9')
}
