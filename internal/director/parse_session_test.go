package director

import (
	"strings"
	"testing"

	"github.com/YiIimini/ManJuX/internal/project"
)

// 专职会话的产出**不许被解析器丢掉**（2026-09-29 真机实测）。
//
// 【现场】第 01 集那一跑，日志里白纸黑字：
//
//	⚠ ④场景提取 会话失败：方案二次解析仍失败: JSON 解析失败: JSON 合法但既无 title 也无 shots;
//	   原文摘录:{"scenes":[{"id":"scene_maintenance_bay","name":"维护舱镜面地板主舱",…
//	⚠ ②人物道具提取 会话失败：… 原文摘录:{"characters":[{"id":"char_qiqi","name":"祁七","kind":"human",…
//	⚠ 源文本的【人物外形锚表】列了 6 个角色，方案里只出了 0 个 —— 已按锚表原文补进：祁七、霜见…
//
// 也就是说：**场景卡与角色卡都生成好了，被解析器判成"非法 JSON"整段扔掉**，方案里于是
// 0 场景、0 角色；角色最后由锚表兜底卡补上（英文提示词只有"a Chinese period-film character,
// aged X to Y"）⇒ 六张定妆照长得几乎一样，白瓷装甲的机器人被画成普通古装人
// （用户报「角色咋都生成一样的了，机器人也按照人来生成？」）。
//
// 判据：**会话形状的 JSON 必须解析成功**；真正的空对象仍然报错。
func TestParsePlanAcceptsSessionShapedJSON(t *testing.T) {
	// ②人物道具的形状（真机原文摘录）
	cast := `{"characters":[{"id":"char_qiqi","name":"祁七","kind":"human",` +
		`"description":"外观二十岁（非人类机体）","image_prompt":"Android girl, porcelain-white armour"}],` +
		`"props":[{"id":"prop_mingpai","name":"钢印铭牌"}]}`
	p, err := parsePlan(cast)
	if err != nil {
		t.Fatalf("②人物道具会话的产出被丢掉了：%v —— 角色卡全丢，定妆照只能靠锚表兜底（长得都一样）", err)
	}
	if len(p.Characters) != 1 || p.Characters[0].Name != "祁七" || len(p.Props) != 1 {
		t.Fatalf("解析出来的内容不对：%+v", p)
	}

	// ④场景的形状
	scene := `{"scenes":[{"id":"scene_maintenance_bay","name":"维护舱镜面地板主舱",` +
		`"description":"归墟环断口深处的旧舰队维护舱主舱。四壁是上千个金属工具抽屉。",` +
		`"image_prompt":"Dead still air in an abandoned orbital maintenance bay"}]}`
	p2, err := parsePlan(scene)
	if err != nil {
		t.Fatalf("④场景会话的产出被丢掉了：%v —— 一集 0 张场景卡，渲染没有环境参考图", err)
	}
	if len(p2.Scenes) != 1 {
		t.Fatalf("解析出来的场景不对：%+v", p2)
	}

	// ⑤分镜的形状（本来就该过）
	board := `{"style":"guofeng_cinematic","shots":[{"id":1,"scene":"s","characters":["祁七"],` +
		`"duration":5,"mode":"ref2va","h3_prompt":"shot one"}]}`
	if _, err := parsePlan(board); err != nil {
		t.Fatalf("分镜形状本来就该过：%v", err)
	}

	// 反面：真正的空对象仍要报错（否则"模型什么都没给"会被当成成功落盘）
	if _, err := parsePlan(`{}`); err == nil {
		t.Fatal("空对象必须报错：否则模型什么都没产出也会被当好方案")
	} else if !strings.Contains(err.Error(), "整份方案为空") {
		t.Fatalf("空对象的报错应说清「整份方案为空」，实得：%v", err)
	}
	// 反面：只有 title 没有 shots 的**完整方案**不在这里判（那是契约门 G002 的活）
	if _, err := parsePlan(`{"title":"某集"}`); err != nil {
		t.Fatalf("只有 title 的方案不该在解析层被拒（缺 shots 由契约门报）：%v", err)
	}
	_ = project.Plan{}
}
