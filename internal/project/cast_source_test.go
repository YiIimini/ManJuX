package project

import (
	"strings"
	"testing"
)

// cast_source_test.go 从原文取"首次出场段"的外形材料。
//
// 夹具 = 《名册之外，八万零一号》全本里的**真机原文照抄**（用户 2026-10-07 问：「小说内容第一次出现角色
// 没有描述角色形象吗？」—— 答案是写了，写得很具体；这四条就是那几段）。每条都留着**它周围的剧情与对话**，
// 因为这道判据的难点恰恰是"只取描写、不取剧情"。
const (
	novelShuangjian = `有人从坡上跳下来。落地的时候他单膝跪了一下，一只手撑在铁皮上。

他站起来。少年，清瘦，肩背不宽，外套的体积大于身体，袖口磨出了毛边。他没有束发，中长过耳直到下颌，额前蓬松微卷的刘海盖过眉毛，几缕碎发遮着眼。冷白的脸上，下颌线清楚。颈侧有一道黑色机械与皮肤交界的接缝纹，正面看得见。双耳各挂着一条细长的银坠，左耳那只歪了，像被人拽过一次。

他右手拇指和食指捏着一块很小的读数板，板上一行冷青色的数在跳。

剥线刀说：“霜见。”

剥线刀说：“你算你的数，别管这个。”

霜见没看他。他先看甲板，再看那三只箱子，最后看剥线刀脚底下。

他说：“数不对。”
`
	novelMohui = `进门的人没有脚步声。黑高领，领口外面扣着一圈颈部金属环；黑色长直发及背，额前中分，发丝贴着两颊，像刚从水里出来。长菱形脸，颧骨微宽，下颌收得很窄；丹凤眼，眼尾上扬，眼上一层冷调的银蓝；法令纹压得深，嘴角是习惯性下压的。

她一进来，柜台后头十来个人一起把手从玻璃卡上收了回来。

不是收进怀里，是收在膝盖上，掌心朝下。收得很整齐。

拾叁站在墙角，小声问苔生：“她是哪个。”

“核验官。”苔生说，“闭嘴。”

墨洄没有看人。她走到柜台上首站住，把手里的玻璃卡在指间转了半圈，才递出去。
`
	novelShaoxian = `第 3482 标准日，早上，核验厅刚换完白班。

邵舷走过来。他走得不快，脚跟着地，肩是含着的；灰蓝长褂的领口扣到最上面一颗，袖口一道名册官袖章。他右手的中指上套着一枚灰银色金属指套，四十毫米长，前端磨得发亮，后缘磨出毛边。

他走到祁七面前站住。他没看她的脸——先看她胸前，再看她的钢印位，最后才把眼睛移到她脸上。
`
	novelYuchiche = `她往大厅走。走到后廊门口那一步，廊子尽头站着一个人。

那个人背对着最里面那盏灭掉的灯，只剩一道深灰的轮廓。深灰的值勤装甲，肩线笔直，两脚并得很齐。他没有往前走，却也没有走开。

廊里没人说话。

然后他开口，语速平，短，声音很轻：“值星长尉迟彻，编号三七二〇四一。”
`
	novelDuanwei = `缆绳在她手心里绷了一下，另一头的人已经转身就走。

灰纪元第八万年，第3470标准日，天没亮。这是她第一次上别人的船。

她跟着走。缆绳在断桅手里是斜的——他背驼，走路右肩低半寸，那根绳就从高处斜到低处，她跟着那个斜度走。链条在她腰上碰了两下，脆的。
`
)

func TestFirstAppearanceExcerptTakesTheNovelsDescription(t *testing.T) {
	cases := []struct {
		text       string
		name       string
		want       []string // 材料里必须出现（真机原文里的原话）
		mustNot    []string // 不许混进来的剧情/对话
		otherNames []string
	}{
		{
			text: novelShuangjian, name: "霜见",
			want:       []string{"外套的体积大于身体", "刘海盖过眉毛", "接缝纹", "银坠"},
			mustNot:    []string{"剥线刀说", "数不对", "读数板", "从坡上跳下来"},
			otherNames: []string{"祁七", "剥线刀"},
		},
		{
			text: novelMohui, name: "墨洄",
			want:       []string{"长菱形脸", "丹凤眼", "高领", "发丝贴着两颊"},
			mustNot:    []string{"柜台", "递出去", "闭嘴", "小声问"},
			otherNames: []string{"祁七", "苔生", "拾叁"},
		},
		{
			text: novelShaoxian, name: "邵舷",
			want:       []string{"灰蓝长褂", "指套", "前端磨得发亮"},
			mustNot:    []string{"钢印位", "胸前"},
			otherNames: []string{"祁七", "苔生"},
		},
		{
			text: novelYuchiche, name: "尉迟彻",
			want:       []string{"深灰的轮廓", "值勤装甲", "肩线笔直"},
			mustNot:    []string{"编号三七二〇四一", "廊里没人说话"},
			otherNames: []string{"祁七", "关棠"},
		},
		{
			text: novelDuanwei, name: "断桅",
			want: []string{"背驼", "右肩低半寸"},
			// "链条在她腰上碰了两下" 就在**被选中的那一段**里（真机原文），它不是外形 —— 逐句筛要滤掉它。
			mustNot:    []string{"另一头的人已经转身就走", "第一次上别人的船", "链条在她腰上碰了两下"},
			otherNames: []string{"祁七"},
		},
	}
	for _, tc := range cases {
		got := FirstAppearanceExcerpt(tc.text, tc.name, tc.otherNames)
		if got == "" {
			t.Fatalf("「%s」应该能从原文取到外形材料（小说首现就写了外形）", tc.name)
		}
		for _, w := range tc.want {
			if !strings.Contains(got, w) {
				t.Errorf("「%s」的材料里应含 %q，实际 %q", tc.name, w, got)
			}
		}
		for _, m := range tc.mustNot {
			if strings.Contains(got, m) {
				t.Errorf("「%s」的材料里混进了剧情/对话（%q）：%q —— 翻译会话会把它译成外貌", tc.name, m, got)
			}
		}
		if n := len([]rune(got)); n > 400 {
			t.Errorf("「%s」的材料过长（%d 字）—— 有上限 400", tc.name, n)
		}
	}
}

// TestFirstAppearanceExcerptRefusesWithoutEvidence 没有外形证据时**宁可不填**。
//
//   - 别人的描写不许当他的（这一段出现的是别的已知角色）；
//   - 那个人的首现全是对话、没有一句外形；
//   - 名字根本不在文本里。
func TestFirstAppearanceExcerptRefusesWithoutEvidence(t *testing.T) {
	// ① 描写段写的是祁七（另一个已知角色），我们的角色只被提到名字。
	othersDesc := `祁七抬手，袖口的白瓷装甲上有一道浅痕。她的刘海被气流掀起一缕。

毕申从门缝后抬起头。
`
	if got := FirstAppearanceExcerpt(othersDesc, "毕申", []string{"祁七"}); got != "" {
		t.Errorf("那一段写的是祁七，不该当成毕申的外形：%q", got)
	}
	// ② 全是对话/动作，没有一句外形。
	noVisual := `毕申说：“那查不了。”

毕申把角标往外推了一指宽。
`
	if got := FirstAppearanceExcerpt(noVisual, "毕申", nil); got != "" {
		t.Errorf("没有外形句时应当返回空串（宁可不填），实际 %q", got)
	}
	// ③ 名字不在文本里。
	if got := FirstAppearanceExcerpt(novelShuangjian, "查无此人", nil); got != "" {
		t.Errorf("名字不在文本里应返回空串，实际 %q", got)
	}
}

// TestFirstAppearanceExcerptFallsThroughToLaterMentions 首现那句是"没内容的提及"时，顺着往下试下一处。
func TestFirstAppearanceExcerptFallsThroughToLaterMentions(t *testing.T) {
	text := `“等毕申来了再说。”苔生说。

毕申把手里的角标推回门缝里。

毕申站在门口。他个子不高，肩有点圆，灰布短褂的袖口磨白了。
`
	got := FirstAppearanceExcerpt(text, "毕申", []string{"苔生"})
	if !strings.Contains(got, "灰布短褂") {
		t.Errorf("第一处提及没有外形材料时应继续往后找，实际 %q", got)
	}
}
