package project

import "testing"

// 家族键必须扛得住**命名式卡片 id**。
//
// 【2026-09-28 用户报「同一个人判成两个 / 两个不同的人判成一个」】
// 旧实现把"能解析的 id"定义成"**以数字结尾**"（`char_01`）——那是早期"角色 id 一律 char_NN"
// 时代的假设。技能侧现在实际产出的卡 id 是**命名式**（`char_bingsheng`、`char_qiqi`），
// 于是家族键**静默退回名字**，两个方向同时出错：
//
//	· 同一个人的两种写法 → 判成两个人（首页角色数虚高、变体排不到主卡后面）；
//	· **两个不同的人同名** → 判成一个（撞脸自检把两人当自己人排除，共用一张脸反而没人报）。
//
// 名字取自用户素材《名册之外，八万零一号》的真实角色。
func TestFamilyKeyHandlesNamedCardIDs(t *testing.T) {
	// ① 有卡 id ↔ 没卡 id（同一个人两张卡）→ 同族
	if !SameCharacter("char_qiqi", "祁七", "", "祁七") {
		t.Error("「char_qiqi/祁七」与「无 id/祁七」是同一个人（一边没写 id），应判同族")
	}
	// ② **两个不同的人同名**（各有自己的 id）→ 不同族。
	//    这是旧实现最隐蔽的错法：撞脸自检会把这两个人当自己人排除掉。
	if SameCharacter("char_zhangsan_a", "张三", "char_zhangsan_b", "张三") {
		t.Error("两个不同的人恰好同名（id 明确不同）不该判成同一个人 —— 否则撞脸自检会漏检")
	}
	// ③ 命名式 id 的家族键不再为空
	if k, _ := CharFamilyKeys("char_bingsheng", "碑生"); k == "" {
		t.Error("命名式卡片 id（char_bingsheng）必须给出家族键，不能退回名字")
	}
	// ④ 造型档仍然要归到主卡（两种写法都认：`_v2` 与 `v2`）
	for _, variant := range []string{"char_qiqi_v2", "char_qiqiv2"} {
		if !SameCharacter("char_qiqi", "祁七", variant, "祁七·少年") {
			t.Errorf("变体卡 %s 应归到主卡 char_qiqi", variant)
		}
	}
	// ⑤ 裸名不是卡片 id（模型随手写的 / 名字被误塞进 ID 字段）→ 不当作家族键
	if k, _ := CharFamilyKeys("张三", "张三"); k != "" {
		t.Errorf("裸名「张三」不是卡片 id，不该给出 id 家族键，实际 %q", k)
	}
}
