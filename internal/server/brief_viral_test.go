package server

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// realEpisodeSource 真机第01集的 source.txt（**照抄原文**，含【开场爆点】后面那个空行）。
//
// 【夹具必须照真实形态写】这一轮的两个缺陷都只在"模型真的这么写"时才出现：
// ① 剧本段在【开场爆点】后有一个空行 ⇒ 按"第一个空行"切只取回 51 字；
// ② 爆点写成旁白、正文从环境与外形描写起手 ⇒ 3 秒爆点根本没被演出来。
// 自己编"两三句意思一下"的夹具两个都测不到。
func realEpisodeSource(t *testing.T) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("testdata", "ep01_source_real.txt"))
	if err != nil {
		t.Fatalf("读真机夹具 testdata/ep01_source_real.txt 失败：%v", err)
	}
	return string(raw)
}

// TestBriefSectionBoundsFollowTheNextSection 【本集剧情】那一段的**读边界**。
//
// 原实现取"第一个空行"（`strings.Index(rest, "\n\n")`）—— 而模型写的剧本在
// 【开场爆点】后面就有一个空行，于是只取回 51 字（整段剧本 2090 字）。
// 后果不是"少读一点"这么轻：完整性判据 `briefIsUsable` 永远判"这份剧本不可用" ⇒
// **每跑一次这一集都重新调 LLM 改编剧本**、把上一稿静默覆盖（连备份都轮不到：
// 备份分支的门槛里写着 len(brief) >= 800）。
func TestBriefSectionBoundsFollowTheNextSection(t *testing.T) {
	src := realEpisodeSource(t)
	brief, ok := episodeBriefOf(src)
	if !ok {
		t.Fatal("真机 source.txt 里应当取得到【本集剧情】")
	}
	n := len([]rune(brief))
	if n < epScriptMinRunes {
		t.Fatalf("只取回 %d 字（真机整段剧本 2090 字）—— 边界又落在【开场爆点】后面那个空行上了；"+
			"这会让判据每轮判\"不可用\"并静默重写剧本", n)
	}
	if n > 3000 {
		t.Fatalf("取回 %d 字 —— 边界把【本集原文】也吃进来了（原文必须在段外）", n)
	}
	for _, want := range []string{"【本集钩子】", "【关键道具与信息】", "【场 4|"} {
		if !strings.Contains(brief, want) {
			t.Errorf("剧情段里应当有 %s（整段剧本都在这段里）", want)
		}
	}
	if strings.Contains(brief, "【本集原文】") || strings.Contains(brief, "第001章") {
		t.Error("剧情段里混进了【本集原文】—— 边界没落在下一个平台节标记上")
	}
	if got := len(briefSceneHeadRE.FindAllString(brief, -1)); got != 4 {
		t.Errorf("真机剧本是 4 场，实得 %d 个场次头", got)
	}
	// 结构**是完整的** —— 这一点必须成立：否则"用户觉得不对劲"会被误当成缺节问题。
	if miss := missingBriefSections(brief); len(miss) != 0 {
		t.Errorf("真机剧本的结构判据不该报缺节，实得 %v", miss)
	}
	// 但**爆款纪律**不过（结构与爆款是两件事，这正是用户那句直觉的机器化）。
	if issue := briefViralIssues(brief); len(issue) == 0 {
		t.Error("真机剧本的爆款纪律违规一条都没报 —— 判据失灵（它在结构上确实全过）")
	}
	if briefIsUsable(brief) {
		t.Error("爆款纪律违规的剧本被判成\"可直接用\" —— 用户要的正是这类稿子按契约重做")
	}
	// 没有【本集原文】标记时（技能侧包/裁过的稿子）：剧情段到文末，不能又只取一句话。
	tail := "《某剧》第01集\n\n【本集剧情】" + goodScriptFixture() + "\n"
	got, ok := episodeBriefOf(tail)
	if !ok || !strings.Contains(got, "【本集钩子】") {
		t.Errorf("没有终止标记时应当取到文末，实得 %d 字（ok=%v）", len([]rune(got)), ok)
	}
}

// TestReplaceEpisodeBriefDoesNotKeepTheOldScript 写入侧的同一处边界。
//
// 原实现与读边界共用"第一个空行"，于是回填写出的是
// `【本集剧情】 + 新稿 + 旧稿从【场 1|…】起的全部内容 + 【本集原文】` ——
// 每重写一轮就多堆一份旧剧本，而【本集原文】前面那段正是分镜要读的输入。
func TestReplaceEpisodeBriefDoesNotKeepTheOldScript(t *testing.T) {
	src := realEpisodeSource(t)
	fresh := goodScriptFixture()
	out := replaceEpisodeBrief(src, fresh)

	if !strings.Contains(out, fresh) {
		t.Fatal("回填后找不到新稿 —— 写入本身就错了")
	}
	if got, want := len(briefSceneHeadRE.FindAllString(out, -1)), len(briefSceneHeadRE.FindAllString(fresh, -1)); got != want {
		t.Errorf("回填后场次数 %d ≠ 新稿的 %d —— 旧稿残留在文件里（分镜会同时读到两份互相冲突的剧本）", got, want)
	}
	if n := strings.Count(out, "【本集原文】"); n != 1 {
		t.Errorf("【本集原文】出现 %d 次（应为 1）", n)
	}
	if !strings.Contains(out, "第001章 重新检测到生命反应") {
		t.Error("回填把【本集原文】的正文吃掉了")
	}
	// 旧稿独有的那句描写不该还在（它在真机旧剧本的第 3 行）
	if strings.Contains(out, "背脊中段缺一块壳，熔接环烧灼痕哑光灰") {
		t.Error("旧稿残留在文件里：新稿后面还跟着旧剧本的正文")
	}
	// 幂等：再回填一次不会越写越长
	twice := replaceEpisodeBrief(out, fresh)
	if twice != out {
		t.Errorf("回填不幂等：第二次写出的长度 %d ≠ 第一次 %d", len([]rune(twice)), len([]rune(out)))
	}
}

// TestViralRulesCatchTheRealScript 爆款纪律判据：真机那份剧本要**被抓住**，合规稿要**放过**。
//
// 夹具是照抄的真机剧本（2082 字、结构全过、爆款纪律全违规）——
// 这条测试就是用户那句「总感觉剧本提取文章内容不是按照爆款短剧要求来的」的机器化表达。
func TestViralRulesCatchTheRealScript(t *testing.T) {
	src := realEpisodeSource(t)
	brief, ok := episodeBriefOf(src)
	if !ok {
		t.Fatal("真机 source.txt 里应当取得到【本集剧情】")
	}
	issues := briefViralIssues(brief)
	joined := strings.Join(issues, "\n")
	t.Logf("真机剧本的爆款违规：\n%s", joined)
	for _, want := range []struct{ needle, why string }{
		{"开场爆点没被演出来", "【开场爆点】写成了旁白（「——灰纪元第八万年，一个不该存在的编号…」），" +
			"引号里那句台词一次没进第 1 场正文，前 3 行全是环境与外形描写"},
		{"大爽点", "全集只有 1 个大爽点（作者口径 2-3 个）"},
	} {
		if !strings.Contains(joined, want.needle) {
			t.Errorf("漏报 %q —— %s", want.needle, want.why)
		}
	}

	// 反向面 ①：合规稿必须一条不报（门不是"见谁咬谁"）
	good := goodScriptFixture()
	if got := briefViralIssues(good); len(got) != 0 {
		t.Errorf("合规稿被误报 %v —— 误报的门会被忽略，等于没有", got)
	}

	// 反向面 ②：把爆点那句台词从第 1 场正文开头挪到第 4 行（前面塞三行铺垫）⇒ 必须报
	moved := strings.Replace(good,
		"  正文:\n  “这册上的字，是我改的。”\n",
		"  正文:\n  碑林的黄昏压下来，风把碎纸吹得贴地跑。\n  围观的人挤在石台前，谁也没说话。\n  邢五抬手一拦，指尖点在册页上。\n  “这册上的字，是我改的。”\n", 1)
	if moved == good {
		t.Fatal("夹具不相符：没找到第 1 场正文的开头（夹具被改过？）")
	}
	if !strings.Contains(strings.Join(briefViralIssues(moved), "\n"), "开场爆点没被演出来") {
		t.Error("爆点被铺垫挤到第 4 行却不报 —— 「无铺垫、无旁白起手」这条等于没有门")
	}

	// 反向面 ③：大爽点降到 1 个 ⇒ 必须报
	fewer := strings.Replace(good, "爽点:大爽点——他逼监工认账", "爽点:小爽点——他逼监工认账", 1)
	if fewer == good {
		t.Fatal("夹具不相符：没找到第 3 场的大爽点那一行")
	}
	if !strings.Contains(strings.Join(briefViralIssues(fewer), "\n"), "大爽点") {
		t.Error("大爽点只剩 1 个却不报 —— 「2-3 个大爽点」这条等于没有门")
	}

	// 反向面 ④：某一场的「钩子:」被漏掉 ⇒ 报**具体哪一场**（段段留钩）
	noHook := strings.Replace(good, "  钩子:链子又响一声，他不知道那枚牌该挂哪里\n", "", 1)
	if noHook == good {
		t.Fatal("夹具不相符：没找到第 3 场的钩子那一行")
	}
	if !strings.Contains(strings.Join(briefViralIssues(noHook), "\n"), "第 3 场缺「钩子」节") {
		t.Error("整篇有「钩子:」但第 3 场没写 ⇒ 旧判据（按全篇找标记）放行；这里必须点出是哪一场")
	}
}

// TestViralRulesAreWiredIntoTheBriefPipeline 接线检查：判据写好了必须**真的用上**。
//
// 2026-10-06 审计里最贵的一类缺陷是"规则写完了没接上，日志一片正常"。
func TestViralRulesAreWiredIntoTheBriefPipeline(t *testing.T) {
	raw := readBriefSource(t)
	for _, want := range []struct{ needle, why string }{
		{"len(briefContractIssues(brief)) == 0", "「已经是一份像样的剧本」的跳过判据必须看**结构 + 爆款纪律**（否则违规稿长驻、每轮不动）"},
		{"briefIsUsable(brief)", "跳过判据要在轮到这一集时被调用"},
		{"briefContractIssues(out)", "生成后按同一份判据决定要不要喂回去补写一轮"},
		{"briefIssueBullets(issues)", "喂回去的那一轮要把违规**逐条**列给模型（只报个数字它改不动）"},
		{"briefContractIssues(fixed)", "补写结果要按同一份判据重新自检"},
		{"adoptRepairedBrief(issues, fixed)", "采纳判据要与自检同源（否则\"结构变好、爆款变差\"也照收）"},
		{"第 1 场正文的最前面", "提示词与补写指令要给**可执行动作**（把爆点那句放第 1 场正文最前面），不是「要有爆点」"},
		{"至少 2 个大爽点", "大爽点数量要写进提示词 —— 判据就是照它判的"},
		{"source-brief-prev.txt", "重写前必须先备份旧稿"},
	} {
		if !strings.Contains(raw, want.needle) {
			t.Errorf("剧本流水线缺 %q —— %s", want.needle, want.why)
		}
	}
}
