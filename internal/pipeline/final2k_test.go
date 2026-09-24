package pipeline

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/YiIimini/ManJuX/internal/project"
)

// "会出像素的路径"闸门(审计 P0-7 / 根因 R4)。
//
// 为什么需要它:`TestShotRenderPromptIsSingleSourceForAllRenderPaths` 只能证明
// **那个函数**是对的,证明不了**所有路径都走它**。而成片 2K 恰好绕过了它 ——
// 一整部哑光动画送去云端按写实皮肤先验重画,付费把片子改坏,而且没有任何测试变红。
//
// 所以闸门扫的是"路径":下表每一条都必须带上介质锁;新增一条出像素路径时,
// 忘了在这里登记的同时也就会忘了带锁 —— 而漏登记本身不影响正确性,漏带锁的**会**
// 被下面的判据抓住(只要它出现在表里)。表的维护纪律写在 docs/架构.md。
func pixFixtures() (*project.Project, *project.Plan, *project.Shot) {
	proj := &project.Project{}
	proj.Config.Params.Script.StylizedFaces = project.Bool(true) // 动漫化(默认)
	plan := &project.Plan{Style: "gritty rural Chinese period realism, 35mm film grain"}
	shot := &project.Shot{ID: 3, H3Prompt: "a young craftsman hammers a bronze nail", Mode: "ref2va"}
	return proj, plan, shot
}

func TestEveryPixelProducingPathCarriesMediumMarker(t *testing.T) {
	proj, plan, shot := pixFixtures()
	paths := map[string]string{
		// 主渲染 / 单镜 2K / 局部重绘 / 音频驱动:四处都走同一个装配函数
		// (stages_render.go、upgrade2k.go 单镜分支、handlers_enhance.go 两处)
		"主渲染与单镜2K与局部重绘与音频驱动(ShotRenderPrompt)": ShotRenderPrompt(proj, plan, shot),
		"成片2K(final2KPrompt)": final2KPrompt(proj, plan),
	}
	for name, p := range paths {
		// 判据用**渲染侧**标记:渲染块允许写 NEGATIVE,图像块不许 —— 两者不可互换
		// (见 RenderMediumMarker 的说明),所以"出像素的路径"该带的是前者。
		if !strings.Contains(p, RenderMediumMarker) {
			t.Errorf("%s 的提示词缺少渲染介质锁标记 %q —— 这条路径会把动画脸重画成写实脸:\n%s",
				name, RenderMediumMarker, p)
		}
		if !strings.Contains(p, "MEDIUM OVERRIDE") {
			t.Errorf("%s 缺少 MEDIUM OVERRIDE 段", name)
		}
	}
	// 反例守卫:两个标记串必须互不相同 —— 否则"这张定妆照是不是动画线"会被渲染侧
	// 的标记误判成真(RealisticForPrompt 是拿 MediumMarker 判的)。
	if MediumMarker == RenderMediumMarker {
		t.Fatal("图像侧与渲染侧的介质标记不能是同一个串")
	}
	if strings.Contains(ShotRenderPrompt(proj, plan, shot), MediumMarker) {
		t.Error("渲染提示词里不该出现仅用于反推定妆照线的图像侧标记")
	}
}

// TestNoPixelPathAsksForSkinDetail 任何出像素的提示词都不得再**要求增加**"皮肤细节"。
//
// 这不是措辞洁癖:`enhance … skin detail` 对动画线是**反向指令**(模型据此补毛孔与
// 油光高光)。判据扫源码而不是扫某个变量 —— 这样"以后新写一条路径时顺手抄了旧文案"
// 也会被抓到。
//
// 判据只打**增强类动词**(enhance/add/boost/sharpen 等)附近出现的 skin detail:
// "copy the … skin details from <Picture 1> exactly" 是多视角的身份**保真**条款
// (要求照抄,不是要求加细节),不该被误伤 —— 闸门误报多了就会被关掉。
func TestNoPixelPathAsksForSkinDetail(t *testing.T) {
	reEnhance := regexp.MustCompile(`(?i)(enhanc\w*|add|adding|increase\w*|boost\w*|sharpen\w*|sharper|more)[^.\n]{0,60}skin[\s-]detail`)
	roots := []string{
		filepath.Join("..", "..", "internal", "pipeline"),
		filepath.Join("..", "..", "internal", "server"),
	}
	scanned := 0
	for _, root := range roots {
		err := filepath.Walk(root, func(p string, info os.FileInfo, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if info.IsDir() || !strings.HasSuffix(p, ".go") {
				return nil
			}
			raw, err := os.ReadFile(p)
			if err != nil {
				return err
			}
			scanned++
			inBlock := false
			for i, ln := range strings.Split(string(raw), "\n") {
				trimmed := strings.TrimSpace(ln)
				if inBlock {
					if strings.Contains(ln, "*/") {
						inBlock = false
					}
					continue
				}
				if strings.HasPrefix(trimmed, "//") {
					continue // 注释里的反面教材(正是本测试要记录的东西)
				}
				if strings.HasPrefix(trimmed, "/*") {
					if !strings.Contains(ln, "*/") {
						inBlock = true
					}
					continue
				}
				if m := reEnhance.FindString(ln); m != "" {
					t.Errorf("%s:%d 要求增强皮肤细节(%q)—— 动画线里这是反向指令(补毛孔/油光),请改成「锐化线稿与平涂边缘」",
						p, i+1, strings.TrimSpace(m))
				}
			}
			return nil
		})
		if err != nil {
			t.Fatalf("扫描失败: %v", err)
		}
	}
	if scanned == 0 {
		t.Fatal("一个 .go 文件都没扫到 —— 路径失效,闸门形同不存在")
	}
}

// TestFinal2KPromptRespectsFacesOff 关掉「角色动漫化」要拍真人剧的项目,不许强塞动画介质。
// 这与 TurnaroundMediumBlock / ViewEditMediumBlock 是同一条判据,三处必须一致。
func TestFinal2KPromptRespectsFacesOff(t *testing.T) {
	proj, plan, _ := pixFixtures()
	proj.Config.Params.Script.StylizedFaces = project.Bool(false)
	p := final2KPrompt(proj, plan)
	if strings.Contains(p, MediumMarker) {
		t.Error("真人剧项目不应被强塞动画介质锁")
	}
	if !strings.Contains(p, "REFINE, DO NOT REDESIGN") {
		t.Error("即使真人写实,也要说清\"这是分辨率提升不是重画\"")
	}
	// 未表态(老项目 json 没有这个键)= 开,与 FacesOn 的语义一致
	proj.Config.Params.Script.StylizedFaces = nil
	if !strings.Contains(final2KPrompt(proj, plan), RenderMediumMarker) {
		t.Error("未表态时应按'开'处理(存量项目正是报障的那批)")
	}
}

// TestFinal2KRefsTakeMainCharacters 成片 2K 必须真的带上参考图(旧实现 refs 声明了
// 却从未赋值 → 连云端的身份锚定都没有),且按出场顺序、有上限、缺文件不阻塞。
func TestFinal2KRefsTakeMainCharacters(t *testing.T) {
	dir := t.TempDir()
	// 造两个真实存在的定妆照文件 + 一个缺失的
	for _, f := range []string{"assets/characters/a.png", "assets/characters/b.png"} {
		p := filepath.Join(dir, f)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("png"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	assets := map[string]project.Asset{
		"char:c1": {ID: "c1", File: "assets/characters/a.png"},
		"char:c2": {ID: "c2", File: "assets/characters/b.png"},
		"char:c3": {ID: "c3", File: "assets/characters/missing.png"},
	}
	plan := &project.Plan{Shots: []project.Shot{
		{ID: 1, Mode: "ref2va", Characters: []string{"c1"}},
		{ID: 2, Mode: "ref2va", Characters: []string{"c1", "c2"}}, // c1 重复:只锚一次
		{ID: 3, Mode: "t2va", Characters: []string{"c3"}},         // 纯文生镜不算锚点
		{ID: 4, Mode: "ref2va", Characters: []string{"c3"}},       // 文件缺失:跳过不阻塞
	}}
	refs := final2KRefs(dir, plan, assets)
	if len(refs) != 2 {
		t.Fatalf("应锚定 2 个存在的角色定妆照,实际 %d:%v", len(refs), refs)
	}
	if refs[0].Path != filepath.Join(dir, "assets/characters/a.png") {
		t.Errorf("应按出场顺序取第一个角色,实际 %s", refs[0].Path)
	}
	if refs[0].Kind != "character" {
		t.Errorf("参考图类型应为 character,实际 %q", refs[0].Kind)
	}
	// 上限:角色再多也不能无限塞(请求体里已经有整条 base_video)
	many := &project.Plan{}
	var ids []string
	for _, id := range []string{"c1", "c2", "c4", "c5", "c6", "c7"} {
		ids = append(ids, id)
	}
	many.Shots = append(many.Shots, project.Shot{ID: 1, Mode: "ref2va", Characters: ids})
	big := map[string]project.Asset{}
	for _, id := range ids {
		f := "assets/characters/" + id + ".png"
		p := filepath.Join(dir, f)
		if err := os.WriteFile(p, []byte("png"), 0o644); err != nil {
			t.Fatal(err)
		}
		big["char:"+id] = project.Asset{ID: id, File: f}
	}
	if got := len(final2KRefs(dir, many, big)); got != final2KMaxRefs {
		t.Errorf("参考图数应被限制在 %d,实际 %d", final2KMaxRefs, got)
	}
	if refs := final2KRefs(dir, nil, assets); refs != nil {
		t.Error("没有分镜时不应返回参考图")
	}
}
