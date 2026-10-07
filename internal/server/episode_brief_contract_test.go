package server

import (
	"fmt"
	"os"
	"strings"
	"testing"
)

// 剧本（① 小说转剧本）必须**结构完整**（2026-09-29 用户：
// 「说明内容转剧本不给力，然后导致分镜脚本不给力」）。
//
// 剧本是分镜**唯一**的输入：少了「目的/冲突/爽点」这一节，分镜就不知道哪一下是重点
// （成片重点平摊＝压不住场）；少了「关键道具与信息」，道具的位置关系就丢
// （实测「Q版总蹲在碑生左肩上」在成片里变成了单独蹲在石台上）。
//
// 判据落在**实际要求模型写出的那几节**上（输出契约逐字照抄，缺节就报），
// 而不是"我改过提示词"。
func TestEpisodeBriefMustCarryItsStructure(t *testing.T) {
	// ① 输出契约必须在系统提示词里（否则模型没有理由按这个结构写）
	raw := readBriefSource(t)
	for _, want := range []struct{ needle, why string }{
		{"【开场爆点】", "3 秒爆点的位置要单独标出（用户文件第一条）"},
		{"【场 1|地点|时间|内外】", "场次头（地点/时间/内外）—— 分镜靠它分场与定机位"},
		{"目的:", "每场的目的 —— 分镜靠它决定「这场要交代什么」"},
		{"爽点:", "每场的爽点（小/大）—— 分镜靠它决定特写与时长给谁"},
		{"钩子:", "每场的钩子 —— 用户文件的「段段留钩」"},
		{"【关键道具与信息】", "道具与信息点清单（含位置，如「蹲在碑生左肩上」）"},
		{"【本集钩子】", "本集终极钩子"},
	} {
		if !strings.Contains(raw, want.needle) {
			t.Errorf("① 剧本的输出契约缺少 %q —— %s", want.needle, want.why)
		}
	}
	// ② 缺节要**如实报出来**（此前是静默的：剧本照样回填、产线照跑，只是分镜凭空猜）。
	// 判据入口是 briefContractIssues（结构 + 爆款纪律，2026-10-07 起）——补写一轮与跳过判据
	// 都必须走这**一个**入口（见 brief_viral.go 的说明）。
	if !strings.Contains(raw, "briefContractIssues(out)") {
		t.Error("剧本回填后没有契约自检（缺节/爆款违规会静默传下去）")
	}
	// ③ 判据本身：完整剧本过、缺节的剧本要报出缺哪几节
	good := "【开场爆点】他改了一个字。\n【场 1|第七坡碑林|日|外】\n目的:立住主角\n冲突:丙十七 vs 邢五\n" +
		"转折:册上的字变了\n爽点:大 —— 当众打脸\n钩子:折了三年寿\n正文:……\n【关键道具与信息】\n- 碑刀|右手|改字\n【本集钩子】册子翻页"
	if miss := missingBriefSections(good); len(miss) != 0 {
		t.Fatalf("完整剧本被误判缺节：%v", miss)
	}
	thin := "【场 1|碑林|日|外】\n正文:他改了个字。" // 上一版实测就长这样：散文，没有结构标注
	miss := missingBriefSections(thin)
	for _, want := range []string{"开场爆点", "目的", "冲突", "爽点", "钩子", "关键道具与信息", "本集钩子"} {
		found := false
		for _, m := range miss {
			if strings.Contains(m, want) {
				found = true
			}
		}
		if !found {
			t.Errorf("缺节自检漏了 %q（实得 %v）", want, miss)
		}
	}
}

// readBriefSource 读 ① 剧本那一步的实现源码（就在同一个包内）。
func readBriefSource(t *testing.T) string {
	t.Helper()
	raw, err := os.ReadFile("novels_jobs.go")
	if err != nil {
		t.Fatalf("读 novels_jobs.go 失败：%v", err)
	}
	return string(raw)
}

// —— ① 剧本这一环的**闭环**（2026-10-06 审计）——
//
// 原状是"生成一次 + 缺节只告警"，两处都错：
//
//	① 「已经在像样的剧本」只看**字数**（≥800 字就跳过）—— 而拆集那一步的**剧情节拍
//	   brief** 被要求 400-700 字、模型常态超发；超发的那一份会被当成剧本长驻，
//	   分镜从此缺「目的/冲突/爽点」全部依据（用户说的"转剧本不给力"有一半来自这里）；
//	② 告警写着"建议重跑本阶段"，而重跑**正是被 ① 那道判据挡住的** —— 给的动作做不到，
//	   比不告警更糟。
//
// 现在：判据是 briefIsUsable（字数 + **结构**），缺结构就备份旧稿后重写；
// 生成后缺节则**喂回去补写一轮**，只采纳"缺节真的变少且不是空稿"的结果
// （与 director.adoptRepaired 同一条纪律）。

// goodScriptFixture 一份**长度与结构都达标、爆款纪律也合规**的本集剧本（照真实形态写：4 场、
// 每场六节、场次头带「地点|时间|内外」、正文是能拍的动作与台词）。
//
// 爆款纪律那三条都要满足（briefViralIssues）：①【开场爆点】里引号那一句**就是第 1 场正文的第
// 一行**（3 秒爆点必须被演出来）② 全集 ≥2 个大爽点（写在「爽点:」节里，逐字写「大爽点」）
// ③ 每一场都写全那五节 + 正文（段段留钩）。
func goodScriptFixture() string {
	var sb strings.Builder
	sb.WriteString("【开场爆点】“这册上的字，是我改的。”——碑生当众认下改字之罪。\n")
	scenes := []struct {
		place, clock, inout string
		purpose             string
		conflict            string
		turn                string
		payoff              string
		hook                string
		body                []string
	}{
		{
			"第七坡碑林", "黄昏", "外",
			"目的:把「碑文能改」这件事立住，让观众立刻知道主角手里有牌",
			"冲突:碑生 vs 邢五 —— 争的是那本名册上到底写着谁的名字",
			"转折:册上的字在他眼皮底下变了，围观的人齐刷刷看过来",
			"爽点:大爽点——当众打脸，邢五的威风被一个字压下去",
			"钩子:他折了三年寿，而这册子还在继续翻页",
			[]string{
				"“这册上的字，是我改的。”",
				"碑生把碑刀握在右手、刃口朝下，一步一步走上石台。",
				"邢五抬手一拦，指尖点在册页上，嘴里的话没说完就卡住了。围观的人先是静，然后一个接一个往前挤。",
			},
		},
		{
			"碑奴号棚通铺", "夜", "内",
			"目的:让观众看到改字的代价，把主角的处境压到最低处",
			"冲突:碑生 vs 押他回来的两名碑卫 —— 争的是把这册子留给谁",
			"转折:棚里那盏灯自己灭了半格，账房先生的名字从册上淡下去",
			"爽点:小爽点——他把碑刀叉在通铺板上，一句“改了的字，谁都擦不掉”压住整棚人",
			"钩子:棚角的老碑奴认出了那枚印，却不敢说出名字",
			[]string{
				"碑刀插进通铺板，刀身还在震。",
				"棚里没人敢先开口，只有灯芯在响。",
				"老碑奴盯着册上那枚印，看了很久，把嘴边的话咽了回去。",
			},
		},
		{
			"核验厅前廊", "清晨", "外",
			"目的:主角不憋屈 —— 他先动手把账摊开，再面对核验官",
			"冲突:碑生 vs 监工 —— 争的是这三年的名声算不算数",
			"转折:监工翻开底册想否认，底册第一页正是他自己的手印",
			"爽点:大爽点——他逼监工认账，把手印按在众目之下",
			"钩子:链子又响一声，他不知道那枚牌该挂哪里",
			[]string{
				"“底册第一页，是你的手印。”",
				"碑生把册页翻开，按在廊柱上，让每个围观的人都看得见。",
				"监工的手停在半空，指节发抖，最后按了下去。",
			},
		},
		{
			"第九坡石亭", "日", "外",
			"目的:钩子拉满 —— 真正的主人在册上露头",
			"冲突:碑生 vs 册子本身 —— 他翻到最后一页",
			"转折:末页那行名字不是他的，是三年后才会写下的人",
			"爽点:小爽点——他合上册子，一步不退，谁也没拦住他",
			"钩子:末页的名字后面，还留着一行空白等着落笔",
			[]string{
				"册子翻到最后一页，纸面比别处新。",
				"碑生把册子合上，抬头看了一眼石亭外的天。",
				"围观的人让开一条路，没有人再拦他。",
			},
		},
	}
	for i, sc := range scenes {
		sb.WriteString(fmt.Sprintf("【场 %d|%s|%s|%s】\n", i+1, sc.place, sc.clock, sc.inout))
		sb.WriteString("  " + sc.purpose + "\n")
		sb.WriteString("  " + sc.conflict + "\n")
		sb.WriteString("  " + sc.turn + "\n")
		sb.WriteString("  " + sc.payoff + "\n")
		sb.WriteString("  " + sc.hook + "\n")
		sb.WriteString("  正文:\n")
		for _, ln := range sc.body {
			sb.WriteString("  " + ln + "\n")
		}
	}
	sb.WriteString("【关键道具与信息】\n- 碑刀|右手，刃口朝下|改字\n- 名册|邢五手里|身份\n")
	sb.WriteString("【本集钩子】册子翻页，露出真正的主人名")
	return sb.String()
}

// proseBeatBriefFixture 拆集那一步的**剧情节拍 brief**（事件与台词要点，散文），
// 被模型写超到 800 字以上的真实形态 —— 它**不是剧本**，但旧判据只看字数就放行。
func proseBeatBriefFixture() string {
	beats := []string{
		"碑生走上石台，邢五抬手拦住，两人隔着那本名册说话。",
		"围观的人低声议论，说这碑上的字从来没人改得动。",
		"邢五翻开册页，念出碑生名字那一段，语气里带着惯有的轻慢。",
		"碑生没有辩解，只把碑刀换到右手，刃口朝下。",
		"册上的字在他眼皮底下变了，围观的人先是一静，然后往前挤。",
	}
	var sb strings.Builder
	for i := 0; i < 40; i++ { // 40 句 ≈ 960 字：模型超发到 800 字以上就是这个量级
		sb.WriteString(beats[i%len(beats)])
	}
	return sb.String()
}

// TestBriefIsUsableNeedsStructureNotJustLength 「已经在像样的剧本」的判据。
func TestBriefIsUsableNeedsStructureNotJustLength(t *testing.T) {
	good := goodScriptFixture()
	if !briefIsUsable(good) {
		t.Fatalf("长度与结构都达标的剧本被误判为不可用（%d 字）—— 会白重写一遍、白烧 token", len([]rune(good)))
	}
	prose := proseBeatBriefFixture()
	if len([]rune(prose)) < epScriptMinRunes {
		t.Fatalf("夹具不真：节拍 brief 必须**超过**字数下限（实得 %d 字 < %d），否则测不到「只看字数就放行」这个缺陷",
			len([]rune(prose)), epScriptMinRunes)
	}
	if briefIsUsable(prose) {
		t.Error("800 字以上的**节拍 brief**（无场次头、无目的/冲突/爽点）被判成可用 —— " +
			"分镜会拿它当剧本用、重点只能自己猜；这正是要挡的那一种")
	}
	short := "【开场爆点】他改了一个字。\n【场 1|碑林|黄昏|外】\n目的:立住主角\n冲突:甲 vs 乙\n" +
		"转折:字变了\n爽点:大 —— 当众打脸\n钩子:折了三年寿\n正文:……\n" +
		"【关键道具与信息】\n- 碑刀|右手|改字\n【本集钩子】册子翻页"
	if briefIsUsable(short) {
		t.Errorf("只有 %d 字的稿子不该算剧本（字数下限 %d）", len([]rune(short)), epScriptMinRunes)
	}
}

// TestRepairedBriefAdoptionIsConservative 补写一轮的采纳判据：宁可留原稿，也不许改坏。
func TestRepairedBriefAdoptionIsConservative(t *testing.T) {
	before := missingBriefSections(proseBeatBriefFixture()) // 节拍 brief：该报的全缺
	if len(before) == 0 {
		t.Fatal("夹具不真：节拍 brief 该被判定缺节")
	}
	if !adoptRepairedBrief(before, goodScriptFixture()) {
		t.Error("补全了（缺节变少、结构完整）却不采纳 —— 这一轮补写等于白做")
	}
	for _, junk := range []string{"", "好的，已按你的要求补全。", "【场 1|碑林|日|外】\n正文:他改了个字。"} {
		if adoptRepairedBrief(before, junk) {
			t.Errorf("空稿或更差的稿子被采纳了：%q —— 自纠不许把产物弄坏", junk)
		}
	}
	// 原来缺 2 节、改完仍缺 2 节（只是换了缺哪两节）：不算变好
	two := []string{"开场爆点", "本集钩子"}
	if adoptRepairedBrief(two, "【场 1|碑林|日|外】\n正文:他改了个字。") {
		t.Error("缺节数量没有变少就不该采纳（换汤不换药）")
	}
}

// TestScriptRepairIsWiredAndBacksUpOld 接线检查：判据要被**真的用上**。
//
// 前几条门证明"判据本身对"；这一条挡的是"判据写好了但没人调用" ——
// 2026-10-06 审计里最贵的一类缺陷（规则写完了没接上，日志一片正常）。
func TestScriptRepairIsWiredAndBacksUpOld(t *testing.T) {
	raw := readBriefSource(t)
	for _, want := range []struct{ needle, why string }{
		{"briefIsUsable(brief)", "「已经在像样的剧本」必须走**长度 + 结构 + 爆款纪律**判据（否则超发的节拍 brief / 爆款违规稿被当剧本用）"},
		{"adoptRepairedBrief(issues, fixed)", "补写一轮的结果必须过采纳判据（否则改坏了也照收）"},
		{"source-brief-prev.txt", "重写旧稿前必须**先备份**（那可能是作者手写或技能侧给的稿子）"},
		{"briefContractIssues(fixed)", "补写后要按**同一份判据**重新自检（不然不知道补上没有）"},
	} {
		if !strings.Contains(raw, want.needle) {
			t.Errorf("剧本闭环缺 %q —— %s", want.needle, want.why)
		}
	}
	// 告警的承诺必须与代码一致：重跑**真的**会重写这一集
	if strings.Contains(raw, "建议重跑本阶段。缺哪节补哪节即可") {
		t.Error("告警还在说「建议重跑本阶段」—— 而重跑会被跳过条件挡住，那句话是做不到的")
	}
	if !strings.Contains(raw, "重跑本阶段会重写这一集") {
		t.Error("缺节告警必须说清「重跑会重写这一集」（这是用户唯一的补救动作）")
	}
}

// TestBriefSectionsAcceptFullWidthColons 节判据必须**两种冒号都认**（2026-10-07 真机实测）。
//
// 现场：那一集的 source.txt 里「目的：」4 处、「目的:」**0** 处 —— 中文模型默认写全角，
// 而判据只认半角，于是报「缺 5 个必需节」（那五节其实一字不缺）。代价不只是假告警：
// 跳过判据 `briefIsUsable` 用的就是它 ⇒ **每跑一次这一集都会重写剧本**、把改过的稿子覆盖掉。
func TestBriefSectionsAcceptFullWidthColons(t *testing.T) {
	// 照真机那份剧本的形态写：全角冒号 + 半角逐字场次头；同时**满足爆款纪律**
	//（爆点那句台词就是第 1 场正文的第一行、两个大爽点、每场五节 + 正文）——
	// 因为 briefIsUsable 现在把爆款纪律也算进去了（见 brief_viral.go），
	// 这一条测的是"冒号宽度不影响判定"，不该被别的规则顺带咬住。
	fullWidth := "【开场爆点】“异动来源，硬件钢印U-80001。”——祁七站在维护舱中央，冷白灯一排排亮起。\n\n" +
		"【场 1|归墟环·维护舱|灰纪元第八万年|内】\n" +
		"  目的：3秒建立绝境——主角是“名册之外”的幽灵。\n" +
		"  冲突：祁七 vs 舱内AI——她要自检，AI以“名册无名”拒绝。\n" +
		"  转折：祁七说出“读我的钢印”，AI静默三秒。\n" +
		"  爽点：大爽点——她一句反问逼得AI停顿，冷白光一排排亮起。\n" +
		"  钩子：AI答“已读取，无对应档位”——名册最后一行是 80000。\n" +
		"  正文：\n  “异动来源，硬件钢印U-80001。”\n  祁七单膝撑地，右手攥着枚发暗的铭牌。\n" +
		"【场 2|维护舱·值班终端|同上|内】\n" +
		"  目的：放大反差——全舱硬件认她，软件名册不认她。\n" +
		"  冲突：祁七 vs 黑色硬壳名册——她亲手翻到最后一页。\n" +
		"  转折：80000 下面一行是空白，连一道格线都没留。\n" +
		"  爽点：大爽点——她把夹页合上，字字咬清，把钢印按在读取板上。\n" +
		"  钩子：AI宣布“七十二标准时”自检窗口。\n" +
		"  正文：\n  祁七走到东头值班终端。\n  翻开黑色硬壳夹页，玻璃纸脆响。\n" +
		"【关键道具与信息】\n- 铭牌|腰侧链子上|身份\n【本集钩子】缝外漆黑，没有光。"
	if miss := missingBriefSections(fullWidth); len(miss) != 0 {
		t.Errorf("全角冒号的剧本被误判缺节 %v —— 那五节其实都在（真机 source.txt 就是这么写的）；"+
			"误判的代价是**每轮重写剧本**、覆盖改过的稿子", miss)
	}
	if !briefIsUsable(fullWidth + strings.Repeat("祁七把铭牌按在腿侧，冷白灯只亮右边一排。", 45)) {
		t.Error("全角冒号 + 字数达标的剧本仍被判不可用 ⇒ briefIsUsable 会每轮重写它")
	}
	// 反向面：**真的缺**那一节仍要报出来（放宽的只是冒号宽度，不是"有没有这一节"）
	missingThin := "【开场爆点】他改了一个字。\n【场 1|碑林|黄昏|外】\n  冲突：甲 vs 乙\n  " +
		"【关键道具与信息】\n- 碑刀|右手|改字\n【本集钩子】册子翻页"
	miss := missingBriefSections(missingThin)
	if len(miss) == 0 {
		t.Fatal("真的缺节反而不报了 —— 那是把判据放宽成了摆设")
	}
	for _, want := range []string{"目的", "转折", "爽点", "钩子"} {
		found := false
		for _, m := range miss {
			if m == want {
				found = true
			}
		}
		if !found {
			t.Errorf("该报出缺 %q，实际 %v", want, miss)
		}
	}
	// 散文里出现「目的」二字但没有冒号 ⇒ 不算写了这一节
	if missingBriefSections("【场 1|碑林|黄昏|外】\n  他抱着某种目的走向碑林，没有人拦他。") == nil {
		t.Error("散文里的「目的」二字被当成了节标记")
	}
}
