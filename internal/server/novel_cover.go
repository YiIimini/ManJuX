package server

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/YiIimini/ManJuX/internal/pipeline"
)

// novel_cover.go 导入脚本时把**小说自己的封面**带进项目。
//
// 用户 2026-09-26:「脚本制作视频使用的封面也要是小说对应的封面才对啊,为什么现在是生成的角色
// 作为封面?」——原因在 projectCover 的兜底顺序:
//
//	自定义封面(assets/cover.*) > 首个角色定妆照 > 首个场景图
//
// 「一键视频创作」(LLM 拆集)那条路会把小说封面复制进 `assets/cover.*`,所以它有封面;
// 而新加的「脚本创作 / 项目包导入」两条入口**没有这一步** —— 导入出来的集全都掉到
// "角色定妆照"这个兜底上,于是封面成了生成的角色。封面是作品的招牌,角色定妆照是**制作中间物**,
// 拿它当招牌等于把草稿贴在门上。
//
// 约定与小说库扫描一致(见 novels.go::scanNovelDir):书目录下 `封面/` 里的第一张图。

// coverImageExts 可当封面的图片后缀。
var coverImageExts = map[string]bool{".png": true, ".jpg": true, ".jpeg": true, ".webp": true}

// coverInDir 取该目录里的封面图(约定 `<书目录>/封面/*.png|jpg|webp`,名字序取第一张)。
func coverInDir(dir string) string {
	ents, err := os.ReadDir(filepath.Join(dir, "封面"))
	if err != nil {
		return ""
	}
	var names []string
	for _, e := range ents {
		if e.IsDir() || strings.HasPrefix(e.Name(), ".") {
			continue
		}
		if coverImageExts[strings.ToLower(filepath.Ext(e.Name()))] {
			names = append(names, e.Name())
		}
	}
	if len(names) == 0 {
		return ""
	}
	sort.Strings(names)
	return filepath.Join(dir, "封面", names[0])
}

// novelCoverFor 找这批包所属那本书的封面。
//
// 两条路都要走:① 小说库根 + 书名(书目录名就是书名,最稳);② 从包根逐级上溯 ——
// 技能端的 `--out` 常常是 `<书>/漫剧脚本/<集包>`,书目录在两三层上面,而上溯必须在
// **小说库范围内**停住(越界就是别人的目录,读它没有道理)。
func (s *Server) novelCoverFor(root, book string) string {
	if r := strings.TrimSpace(s.novelRoot()); r != "" && strings.TrimSpace(book) != "" {
		if p := coverInDir(filepath.Join(r, strings.TrimSpace(book))); p != "" {
			return p
		}
	}
	dir := filepath.Clean(root)
	if dir == "" || dir == "." || !s.inNovelRoot(dir) {
		return ""
	}
	for {
		if p := coverInDir(dir); p != "" {
			return p
		}
		parent := filepath.Dir(dir)
		if parent == dir || !s.inNovelRoot(parent) {
			return ""
		}
		dir = parent
	}
}

// applyNovelCover 把小说封面写进这批项目(每集 `assets/cover.<ext>`)。
//
// 原则:**只在项目还没有封面时写**。用户手动上传的自定义封面优先级最高(projectCover 也这么排),
// 平台不该用书封面把它顶掉 —— 那是"我明明换过封面,它又变回去了"这类最难查的形态。
// 返回写了几个项目,并播报来源(封面写成没有,用户一眼能看出是不是走了兜底)。
func (s *Server) applyNovelCover(book string, ids []string, root string) int {
	if len(ids) == 0 {
		return 0
	}
	src := s.novelCoverFor(root, book)
	if src == "" {
		s.noteMissingNovelCover(strings.TrimSpace(book))
		return 0
	}
	ext := strings.ToLower(filepath.Ext(src))
	b, err := os.ReadFile(src)
	if err != nil {
		s.opLog("⚠ 小说封面读不到(%s):%v", src, err)
		return 0
	}
	n := 0
	for _, id := range ids {
		dir := filepath.Join(s.store.Dir(id), "assets")
		if hasAnyCover(dir) {
			continue // 已自定义封面:不动
		}
		if err := os.MkdirAll(dir, 0o755); err != nil {
			continue
		}
		if err := os.WriteFile(filepath.Join(dir, "cover"+ext), b, 0o644); err != nil {
			s.opLog("⚠ 写入封面失败(%s):%v", id, err)
			continue
		}
		n++
	}
	if n > 0 {
		s.opLog("🎬 已把小说封面写进 %d 个集项目封面:%s → assets/cover%s", n, src, ext)
		s.bus.Publish(pipeline.Event{Phase: "novel", Level: "success",
			Message: "🎬 《" + strings.TrimSpace(book) + "》封面已应用到 " + strconv.Itoa(n) + " 个集(来自小说库封面图)"})
	}
	return n
}

// hasAnyCover 项目是否已有封面(自定义封面优先,不覆盖)。
func hasAnyCover(assetsDir string) bool {
	ents, err := os.ReadDir(assetsDir)
	if err != nil {
		return false
	}
	for _, e := range ents {
		if e.IsDir() {
			continue
		}
		n := strings.ToLower(e.Name())
		if strings.HasPrefix(n, "cover.") && coverImageExts[filepath.Ext(n)] {
			return true
		}
	}
	return false
}

// missingCoverMsg 与 noteMissingNovelCover:「这本书没有封面图」这件事**只有一句话、一个级别**。
//
// 【为什么要收成一处·2026-10-10】两条入口（「一键视频创作」LLM 拆集 / 「脚本创作·包导入」）都会
// 遇到"书没有封面图"，而平台此前**两处口径不同**：包导入那条会如实说一句，而一键视频创作那条
// 在 `applyCover` 里是 **`if cover == "" { return }` 静默跳过** —— 一集集建出来全都没有封面，
// 用户完全不知道原因（这正是 2026-09-29 审计同型缺陷的另一半：那次修的是"写失败要报"，
// 这次修的是"**根本没有来源**也要报"）。同一个问题在两条路径上说起来像两个不同的问题，
// 就是这条规矩（AGENTS.md：口径只许有一份）要防的事。
//
// 级别取 **warn**：不是错误，但必须让用户看见 —— 封面是作品的招牌，缺了它集项目会退到
// 「首个角色定妆照」（制作中间物）兜底。
func missingCoverMsg(book string) string {
	return fmt.Sprintf("⚠ 《%s》没有封面图（小说库约定 `<书目录>/封面/*.png`）——集项目封面会退到"+
		"「首个角色定妆照」兜底。两个办法：① 放一张 `<书目录>/封面/封面.png` 再重新导入；"+
		"② 用小说库的「封面重做」出图 → 点「用这张」（它会一并刷新这本书的集项目封面）", book)
}

func (s *Server) noteMissingNovelCover(book string) {
	s.bus.Publish(pipeline.Event{Phase: "novel", Level: "warn", Message: missingCoverMsg(book)})
}
