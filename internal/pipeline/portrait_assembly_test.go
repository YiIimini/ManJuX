package pipeline

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/YiIimini/ManJuX/internal/project"
)

// 定妆照装配"唯一实现点"闸门(审计 P1-2 / 根因 R1)。
//
// 起因:同一次装配(normalizeUniformWording → 取景重写 → styleExpand →
// castContrastBlock → uniformCivilianClarify)曾在**四处**各写一遍,幂等判据还各不
// 相同("RENDER STYLE" / "CAST DISTINCTNESS" / 类型标记串)。于是"重新生成的那张
// 定妆照"与产线那张不是同一套约束 —— 而它们是同一个角色的脸,不一致就是换脸。
//
// 判据:非测试源码里,只能有 PortraitPrompt 一处调用这些装配函数;
// characterImagePrompt / LibraryPortraitPrompt / PortraitPromptForProject 都是它的
// 委托包装(允许存在,但它们内部不得再自己拼这些东西)。
// 装配链上的调用:`rewriteSheetFraming` / `styleExpand` / `castContrastBlock` /
// `uniformCivilianClarify` / `normalizeUniformWording`。
//
// 其中只有前三个是**定妆照装配专有**(别处调用它没有意义);后两个是文本级工具,
// 分镜锚定段(描述卡司)也合法复用,所以"单实现点"的严格计数只打前三个,
// 全部五个都参与"不得在别的文件里自己拼"的文件级扫描。
var assemblyCalls = []string{
	"normalizeUniformWording(",
	"styleExpand(",
	"styleExpandFor(",
	"castContrastBlock(",
	"uniformCivilianClarify(",
	"rewriteSheetFraming(",
}

// portraitOnlyCalls 只在装配点里允许出现(装配专有)。
var portraitOnlyCalls = []string{
	"styleExpand(",
	"styleExpandFor(",
	"castContrastBlock(",
	"rewriteSheetFraming(",
}

// assemblyOwners 允许调用这些装配专有函数的**函数**(= 允许存在的装配点)及其豁免。
//
// 2026-09-26 乙1 前这里只允许一处(PortraitPrompt)。现在场景参考图也必须走同一套
// 风格装配(否则"角色一套画风、场景另一套"),所以**允许两个唯一装配点**:
//   - PortraitPrompt   —— 角色定妆照(脸/器物/灵宠/妖兽/无实体/Q 版);
//   - SceneImagePrompt —— 场景环境图(取景是空场环境图,忠实度落在空间结构上,
//     所以**不许**带 castContrastBlock:那是"同剧角色别撞脸"的对比块,对场景无意义)。
//
// 判据从"全文件计数 ≤1"改成"逐函数计数":每个调用只能出现在它的合法归属函数里、
// 且恰好一次 —— 这样既不挡住第二个装配点,也不会让"随手在别处再拼一遍"混进来。
var assemblyOwners = map[string][]string{
	// styleExpand 是画风块的**无上下文入口**(等价于"提示词里还没有技能侧比例句"),
	// 本体只有一行委托。装配点必须走 styleExpandFor —— 它把已装配的提示词带进去,
	// 好让平台自己的比例措辞在技能侧比例锚点句同现时让位(技能侧清单 §5.1)。
	"styleExpand":      {"styleExpandFor("},
	"PortraitPrompt":   {"styleExpandFor(", "castContrastBlock(", "rewriteSheetFraming("},
	"SceneImagePrompt": {"styleExpand(", "rewriteSheetFraming("},
}

// goTopLevelFuncs 把源码里的**顶层函数**切成 函数名 → 函数体行。
// 只认行首的 `func`(方法带接收者也算),嵌套闭包落在调用方函数体内,不影响计数。
func goTopLevelFuncs(src string) map[string][]string {
	out := make(map[string][]string)
	name := ""
	for _, ln := range strings.Split(src, "\n") {
		if strings.HasPrefix(ln, "func ") {
			name = goFuncName(ln)
		}
		if name == "" {
			continue
		}
		out[name] = append(out[name], ln)
	}
	return out
}

var reFuncName = regexp.MustCompile(`^func\s+(?:\([^)]*\)\s+)?([A-Za-z_][A-Za-z0-9_]*)`)

// goFuncName 从 `func [接收者]名字(...)` 里取函数名(取不到返回空串)。
func goFuncName(line string) string {
	m := reFuncName.FindStringSubmatch(line)
	if len(m) < 2 {
		return ""
	}
	return m[1]
}

func TestPortraitAssemblyHasOneImplementation(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	scanned := 0
	var offenders []string
	// PortraitPrompt 本体所在的文件允许出现这些调用;其它文件一律不许
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") || f == "prompts.go" {
			continue
		}
		raw, rerr := os.ReadFile(f)
		if rerr != nil {
			t.Fatal(rerr)
		}
		scanned++
		for i, ln := range strings.Split(string(raw), "\n") {
			trimmed := strings.TrimSpace(ln)
			if strings.HasPrefix(trimmed, "//") {
				continue
			}
			for _, call := range assemblyCalls {
				if strings.Contains(ln, call) {
					offenders = append(offenders, f+":"+itoa2(i+1)+"  自己拼装 "+call+"...)")
				}
			}
		}
	}
	if scanned == 0 {
		t.Fatal("没扫到任何源码文件 —— 闸门失效")
	}

	// prompts.go 里也只在**两个装配点**(PortraitPrompt / SceneImagePrompt)里各调用一次
	raw, err := os.ReadFile("prompts.go")
	if err != nil {
		t.Fatal(err)
	}
	bodies := goTopLevelFuncs(string(raw))
	if _, ok := bodies["PortraitPrompt"]; !ok {
		t.Fatal("没切出 PortraitPrompt 的函数体 —— 闸门本身失效了")
	}
	if _, ok := bodies["SceneImagePrompt"]; !ok {
		t.Fatal("没切出 SceneImagePrompt 的函数体 —— 场景装配点不见了(乙1 回归)")
	}
	for fn, lines := range bodies {
		for _, call := range portraitOnlyCalls {
			n := 0
			for _, ln := range lines {
				trimmed := strings.TrimSpace(ln)
				// 跳过注释与**函数定义行**(`func styleExpand(...)` 里也含这个名字)
				if strings.HasPrefix(trimmed, "//") || strings.HasPrefix(trimmed, "func ") {
					continue
				}
				n += strings.Count(ln, call)
			}
			if n == 0 {
				// 反向校验:声明的装配点里**必须真的**有这一句 —— 否则闸门会因为
				// 函数改名/调用删掉而变成空转(它自己失效了却永远绿)。
				if contains(assemblyOwners[fn], call) {
					offenders = append(offenders, "prompts.go:"+fn+" 里找不到 "+call+
						"...) —— 装配点声明与实现对不上,闸门已失效")
				}
				continue
			}
			if !contains(assemblyOwners[fn], call) {
				offenders = append(offenders, "prompts.go:"+fn+" 里有 "+itoa2(n)+" 处 "+call+
					"...) —— 装配专有函数只允许在装配点里调用(PortraitPrompt / SceneImagePrompt)")
				continue
			}
			if n > 1 {
				offenders = append(offenders, "prompts.go:"+fn+" 里有 "+itoa2(n)+" 处 "+call+
					"...) —— 一个装配点只应调用一次")
			}
		}
	}
	if len(offenders) > 0 {
		t.Errorf("定妆照/场景图装配只能各有一个实现点,以下地方在自己拼:\n  %s",
			strings.Join(offenders, "\n  "))
	}
}

// contains 小工具(避免为一个判据引入 slices 依赖)。
func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

// TestPortraitPromptIsIdempotent 幂等判据只有一个:该类型的 SheetMarker。
// 重复调用(重生成 / 渲染前补齐 / 库复用)不得叠加。
func TestPortraitPromptIsIdempotent(t *testing.T) {
	// 人:装配一次后再装配,风格块与忠实度块都只应出现一次
	sub := PortraitSubject{
		Kind: KindHuman, Name: "小凡", Description: "十七岁少年,灰布短褂",
		ImagePrompt: "a slim seventeen-year-old boy in a grey robe", FacesOn: true,
	}
	plan := &project.Plan{Style: "国漫二维动画", Characters: []project.Character{{ID: "c1", Name: "小凡"}}}
	once := PortraitPrompt(sub, plan)
	twice := PortraitPrompt(PortraitSubject{
		Kind: KindHuman, Name: sub.Name, Description: sub.Description,
		ImagePrompt: once, FacesOn: true,
	}, plan)
	if twice != once {
		t.Errorf("第二次装配不得改动结果(幂等):\n一次:%s\n二次:%s", once, twice)
	}
	if n := strings.Count(twice, "RENDER STYLE"); n != 1 {
		t.Errorf("风格块应恰好 1 份,实际 %d", n)
	}
	// 资产库那条路(无项目上下文)同样幂等
	lib := LibraryPortraitPrompt("阿凿", "巴掌大的机关铜雀", "a palm-sized mechanical bronze sparrow")
	if again := LibraryPortraitPrompt("阿凿", "巴掌大的机关铜雀", lib); again != lib {
		t.Error("资产库装配也必须幂等")
	}
}

// TestPortraitPromptPerKind 实体类型决定取景与忠实度:器物不得被要求"半身正面"。
func TestPortraitPromptPerKind(t *testing.T) {
	raw := "a palm-sized mechanical bronze sparrow, plain background, upper-body framing, front-facing"
	obj := PortraitPrompt(PortraitSubject{
		Kind: KindObject, Name: "阿凿", Description: "机关铜雀", ImagePrompt: raw, FacesOn: true,
	}, nil)
	low := strings.ToLower(obj)
	if strings.Contains(low, "upper-body") || strings.Contains(low, "front-facing") {
		t.Errorf("器物必须重写人像取景,否则又会长成半身人像:\n%s", obj)
	}
	// 人:不重写(LLM 写的人像取景本来就对)
	human := PortraitPrompt(PortraitSubject{
		Kind: KindHuman, Name: "小凡", Description: "少年", ImagePrompt: raw, FacesOn: true,
	}, nil)
	if !strings.Contains(strings.ToLower(human), "upper-body") {
		t.Errorf("人的取景不该被改写:\n%s", human)
	}
}

var _ = os.ReadFile // 保持 import 稳定

func itoa2(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}
