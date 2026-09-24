package pipeline

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/YiIimini/ManJuX/internal/project"
)

// 种子派生收口闸门(审计 P1-2 / 根因 R1)。
//
// 起因:种子的派生方式曾有四种(nameSeed / 长度哈希 / time.Now / 绕过),
// 后果分两类且都很难查:
//   - **不稳定**(时间随机):同一镜重渲得到另一张脸,续跑判据也对不上;
//   - **碰撞**(长度哈希 `len(id)*1000+len(name)*7`):两个名字长度相同的角色
//     撞到同一个种子 → 生成同一张图。Q 版一直用的是这个,而主定妆照早就改成
//     fnv 全串哈希了。
//
// 现在只有三个出口:nameSeed(稳定)、ShotSeed(镜头)、RandomSeed(显式要换一张)。
func TestSeedDerivationHasOneExit(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	scanned := 0
	var offenders []string
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") || f == "stages_assets.go" { // 出口定义在本文件
			continue
		}
		if f == "library_ops.go" { // server 侧:由 server 包自己的闸门覆盖
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
			if strings.Contains(ln, "time.Now().UnixNano") {
				offenders = append(offenders, f+":"+itoa3(i+1)+"  直接取时间当种子 —— 请用 RandomSeed()")
			}
			// 长度哈希:同长度的名字必撞
			if strings.Contains(ln, "int64(len(") {
				offenders = append(offenders, f+":"+itoa3(i+1)+"  长度哈希当种子(同长度必撞)—— 请用 nameSeed()")
			}
		}
	}
	if scanned == 0 {
		t.Fatal("没扫到任何源码文件 —— 闸门失效")
	}
	if len(offenders) > 0 {
		t.Errorf("种子派生只能走 nameSeed / ShotSeed / RandomSeed:\n  %s",
			strings.Join(offenders, "\n  "))
	}
}

// TestShotSeedStability 镜头种子必须可复现:同一镜同参数 → 同一个值;
// 换镜、换角色组合、换覆盖值都必须变。
func TestShotSeedStability(t *testing.T) {
	chars := []string{"c1", "c2"}
	a := ShotSeed(0, 0, 3, chars)
	if b := ShotSeed(0, 0, 3, chars); a != b {
		t.Fatal("同参数必须得到同一种子(否则断点续跑会重渲)")
	}
	if ShotSeed(0, 0, 4, chars) == a {
		t.Error("不同镜号必须得到不同种子")
	}
	if ShotSeed(0, 0, 3, []string{"c1", "c3"}) == a {
		t.Error("不同角色组合应得到不同种子(换角色组合就是另一组脸)")
	}
	if ShotSeed(0, 99, 3, chars) == a {
		t.Error("种子覆盖必须生效")
	}
	if ShotSeed(12345, 0, 3, chars) == a {
		t.Error("项目固定种子必须生效")
	}
	// 覆盖优先于项目种子
	if ShotSeed(12345, 99, 3, chars) != ShotSeed(0, 99, 3, chars) {
		t.Error("单镜覆盖应优先于项目固定种子")
	}
}

// TestChibiSeedDoesNotCollide 回归:Q 版种子原先用长度哈希,两个"长度相同"的角色
// 会撞到同一个种子(生成同一张图)。这条用例正是那个坑的形状。
func TestChibiSeedDoesNotCollide(t *testing.T) {
	// 两个字符数相同但内容不同的 id/名字
	pairs := [][2]string{
		{"char_luqingyan", "char_luqingyao"}, // 同长度、差一个字符
		{"char_ab", "char_cd"},               // 短 id
		{"一个五字角色名", "另一个五字角色"},               // 中文同长度
	}
	for _, p := range pairs {
		if nameSeed("chibi", p[0]) == nameSeed("chibi", p[1]) {
			t.Errorf("%q 与 %q 的 Q 版种子相撞 —— 会生成同一张图", p[0], p[1])
		}
	}
	// 同输入必须稳定(跨集复用同一张 Q 版)
	if nameSeed("chibi", "c1") != nameSeed("chibi", "c1") {
		t.Error("同名必须同种子(跨集一致性)")
	}
	// 不同用途即使同名也不该相撞(前缀区分)
	if nameSeed("chibi", "c1") == nameSeed("shot", "c1") {
		t.Error("不同 kind 前缀应得到不同种子")
	}
}

// TestAssetSeedMatchesName 资产种子与名字绑定的语义(换名字 = 换图,同名 = 同图)。
func TestAssetSeedMatchesName(t *testing.T) {
	if nameSeed("characters", "张三") != nameSeed("characters", "张三") {
		t.Fatal("同名资产必须同种子")
	}
	if nameSeed("characters", "张三") == nameSeed("characters", "李四") {
		t.Fatal("不同名字应有不同种子")
	}
	_ = project.Asset{}
}

func itoa3(n int) string {
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
