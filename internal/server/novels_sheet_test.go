package server

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// 人物外形锚表必须落在**每一集**的分镜输入上(2026-09-27 真机实证后的闸门)。
//
// 【为什么要有这条】锚表最早接在 `handleNovelProduce` 的 `text` 上,而那份 text 只喂
// **拆集**(写 brief);每集项目的 `source.txt` 是 `《系列》…【本集剧情】brief ＋【本集原文】`,
// 锚表根本不在里面 —— 于是脚本阶段的 LLM 看到的仍是一份没有外貌信息的原文,角色卡照旧现编。
// 实测《求你们骂我》第 01 集:项目里沈砚被写成「二十四岁 / 头顶高髻 / 灰褐色短褐 / 左眉尾疤」,
// 而技能侧的锚表写的是「十九岁 / 低束发、中短及下颌 / 洗得发白的灰白粗布短打 /
// 左手腕墨色纹路 + 右前臂新刀口」——**定妆照按"现编的那个人"出图,全片身份锚从一开始就错了**。
//
// 这类缺陷不会报错、也不会让任何一步失败:它只是"约束没生效",而"没生效"看起来和
// "生效了但模型没照做"一模一样 —— 只有针对性断言能钉住。
func TestEpisodeSourceCarriesCharacterSheet(t *testing.T) {
	const sheet = "### 沈砚（主角 · 灾星）\n- 年龄：十九岁\n- 发型三要素：低束发＋中短＋额前全部向后梳净\n"
	base := "《求你们骂我》系列短视频 第01 集《求你们骂我》\n全局风格:GUOFENG\n\n【本集剧情】节拍一…\n\n【本集原文】正文…"

	// ① 没有锚表 → 一个字都不动(存量书没有这张表,行为必须与接入之前完全一致)
	if got := episodeSourceWithSheet(base, ""); got != base {
		t.Errorf("没有锚表时不该改动 source,实际:\n%s", got)
	}
	if got := episodeSourceWithSheet(base, "   \n "); got != base {
		t.Errorf("空白锚表同样不该改动 source,实际:\n%s", got)
	}

	// ② 有锚表 → 抬头 + 锚表正文都进去,且落在**剧情之前**(硬约束要早于剧情出现)
	got := episodeSourceWithSheet(base, sheet)
	if !strings.Contains(got, characterSheetHeader) {
		t.Error("拼进去的锚表缺「硬约束」抬头 —— 没有抬头它就只是「参考背景资料」,模型不会照抄")
	}
	if !strings.Contains(got, "十九岁") || !strings.Contains(got, "低束发") {
		t.Errorf("锚表正文没拼进去:\n%s", got)
	}
	iSheet := strings.Index(got, characterSheetHeader)
	iPlot := strings.Index(got, "【本集剧情】")
	if iSheet < 0 || iPlot < 0 || iSheet > iPlot {
		t.Errorf("锚表必须排在【本集剧情】之前(它是硬约束,不是补充说明):sheet@%d plot@%d", iSheet, iPlot)
	}
	// 抬头那句"这是什么剧的第几集"仍要在最前(分集上下文的第一句)
	if !strings.HasPrefix(got, "《求你们骂我》系列短视频 第01 集") {
		t.Errorf("拼接不该顶掉分集抬头,实际开头:%s", firstLine(got))
	}

	// ③ 幂等:补缺模式重跑 / 已有集回填都会再走一次这条路,叠两份就是 token 白烧 + 口径重复
	if twice := episodeSourceWithSheet(got, sheet); twice != got {
		t.Error("重复拼接会叠第二份锚表(幂等判据是抬头,不许绕开它)")
	}
}

// 硬约束抬头**恰好一份**(2026-09-29 实测:每集 source.txt 里印着两份)。
//
// 成因是两处各拼一遍抬头:readCharacterSheet 自己拼一份,而它的唯一消费方
// episodeSourceWithSheet 的幂等判据正是"src 里有没有抬头" —— 判据看不到
// 刚从函数里出来的那份,于是又拼一份。读 source.txt 的人(以及分镜 LLM)
// 看到的是"这段硬约束被强调了两遍",而它本来就是靠"只讲一次、讲清楚"生效的。
func TestEpisodeSourceHasSingleSheetHeader(t *testing.T) {
	dir := t.TempDir()
	sd := filepath.Join(dir, "设定集")
	if err := os.MkdirAll(sd, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sd, "人物外形锚表.md"),
		[]byte("沈砚:瘦长脸,肩背挺直,低束发、及肩胛。\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	sheet, _ := readCharacterSheet(dir)
	src := "《名册之外，八万零一号》系列短视频 第01 集《重新检测到生命反应》\n全局风格:G\n\n【本集剧情】节拍…\n\n【本集原文】正文…"
	got := episodeSourceWithSheet(src, sheet)
	if n := strings.Count(got, characterSheetHeader); n != 1 {
		t.Errorf("硬约束抬头应恰好一份,实得 %d 份:\n%s", n, got)
	}
	if !strings.Contains(got, "低束发、及肩胛") {
		t.Errorf("锚表正文没拼进去:\n%s", got)
	}
}

// 接线闸门:两处调用点都必须把锚表**传到位** —— 一处是"从小说库生产"(能取到目录),
// 一处是 createSeg(真正拼进 source 的地方)。删掉任何一处,上面那条实证就会复发。
func TestCharacterSheetIsWiredIntoEpisodeSource(t *testing.T) {
	produce, err := os.ReadFile("novels_produce.go")
	if err != nil {
		t.Fatal(err)
	}
	split, err := os.ReadFile("novels_split.go")
	if err != nil {
		t.Fatal(err)
	}
	ps, ss := string(produce), string(split)
	for _, want := range []string{
		"sheet, sheetCut := readCharacterSheet(in.Path)",                       // 读小说目录下的设定集(带限长说明)
		"s.splitNovelToProjects(splitCtx, name, text, maxEp, artStyle, sheet,", // 传给拆集
		"episodeSourceWithSheet(src, sheet)",                                   // 由拆集拼进每一集
		"Level: \"warn\", Message: sheetCut",                                   // 限长说明必须**如实播报**,不许静默少喂几个角色
	} {
		if !strings.Contains(ps, want) && !strings.Contains(ss, want) {
			t.Errorf("缺少接线 %q —— 锚表又只走到拆集输入上,角色卡会退回现编", want)
		}
	}
	// 反面:锚表不许再拼回拆集输入(text)—— 那正是"看着接上了、其实没到分镜"的老写法
	if strings.Contains(ps, "text = sheet + ") {
		t.Error("锚表不该再拼到拆集输入 text 上:那条路到不了脚本阶段(见 episodeSourceWithSheet 的实证)")
	}
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}
