package pipeline

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/YiIimini/ManJuX/internal/project"
)

// 书级 roster 必须**按卡片 id 去重**。
//
// 【2026-09-28 审计 B5】原实现按**名字**去重（`seenC[名字]`）—— 同名不同人时
// **第二个人整个被丢掉**：它的描述根本不进名单，于是本集里那个同名的角色拿不到自己的
// 设定，只能被模型按第一个人的形象写（锚到别人身上）；而平台侧一声不吭。
func TestBookRosterKeepsSameNameDifferentCards(t *testing.T) {
	root := t.TempDir()
	store, err := project.NewStore(root)
	if err != nil {
		t.Fatal(err)
	}
	// 兄弟集：第 01 集已确立两个**同名但不同**的角色（老张 / 小张），外加一个普通人。
	mk := func(id string, chars ...project.Character) {
		dir := store.Dir(id)
		if err := os.MkdirAll(filepath.Join(dir, "plan"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := store.SavePlan(id, &project.Plan{Characters: chars}); err != nil {
			t.Fatal(err)
		}
	}
	const ep1 = "测试剧·第01集"
	const ep2 = "测试剧·第02集"
	mk(ep1,
		project.Character{ID: "char_laozhang_a", Name: "老张", Description: "五十岁的修表匠，右手有旧烫伤"},
		project.Character{ID: "char_laozhang_b", Name: "老张", Description: "二十岁的快递员，寸头"},
		project.Character{ID: "char_li", Name: "李四", Description: "杂货铺老板"},
	)
	mk(ep2, project.Character{ID: "char_li", Name: "李四", Description: "杂货铺老板"})

	c := &Coordinator{store: store}
	text, n := c.bookRoster(ep2)
	if n != 3 {
		t.Fatalf("第 01 集有 3 张角色卡（含两个同名不同人），roster 该收 3 条，实际 %d 条：\n%s", n, text)
	}
	// 两个"老张"的描述**都要在**，而且都要能被区分
	if !strings.Contains(text, "修表匠") || !strings.Contains(text, "快递员") {
		t.Errorf("同名不同人的两条描述都必须进名单（丢掉一条 = 那个人锚到别人身上）：\n%s", text)
	}
	// 并且必须**告诉模型这是两个人** —— 否则它会把两条同名记录当成同一个人的两种写法合并
	if !strings.Contains(text, "同名") || !strings.Contains(text, "不同") {
		t.Errorf("同名不同人时必须显式提示模型「这是两个角色」：\n%s", text)
	}
}
