package project

import "testing"

// cn_mirror_test.go `StripCNMirror`：**中英对照块不进提交版**这条口径的判据本体。
//
// 【为什么要有这一条（2026-10-12，用户给了一篇公众号实测文，要求按它排查"画面里凭空多出字幕"）】
// 拿真机夹具走**真实装配**逐镜量（`<d>` 外、主体名已扣除的可避免汉字）：
//
//	plan_real_voiceover.json       夹具原文 0  → 装配后每镜 1502
//	skill-produced-plan-ep01.json  夹具原文 6  → 装配后每镜 395
//
// 而真机夹具**自己是干净的** —— 那 1502 个字全部是平台在装配时追加进去的
// （`EnsureOnScreenTextRule` / `EnsureSpokenLinesClause` / `EnsureSpeechPlacementClause` /
// 参考图清单 / 转场字幕 … 每一条都带一份 `(中文对照:……)`）。
// 本文件所属的判据说得很清楚：「`<d>` 块外的汉字 = 往画面上写字」，导入侧 >200 直接 fail；
// 图像侧也早就统一清了（`DropHanRunes`）—— 只有**视频侧**一直没清。
func TestStripCNMirror(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
		// keepCJK 期望值里本来就带着必须保留的汉字（`<d>` 台词 / 主体名）。
		keepCJK bool
	}{
		{
			// 真机形态：英文规则 + 换行 + 独占一行的对照块（半角括号）
			name: "半角括号的对照块独占一行",
			in: "3. No burned-in subtitles, no captions, no watermarks.\n" +
				"(中文对照:③ **不许烧任何字幕/说明条/水印**:本片的字幕与系统面板一律由后期合成。)\n",
			want: "3. No burned-in subtitles, no captions, no watermarks.\n",
		},
		{
			// 真机形态：全角冒号 + 全角括号
			name: "全角括号与全角冒号",
			in:   "SPOKEN LINES ONLY.\n（中文对照：本镜没有任何台词 —— 不许自己造人声。）\n",
			want: "SPOKEN LINES ONLY.\n",
		},
		{
			// 对照块自己带括号：必须整块吃掉（贪心到行内最后一个右括号），
			// 只吃一半会留下 `, ; **** ****, )` 那种残渣，还会破坏幂等。
			name: "对照块内部还有括号",
			in:   "STYLE AUTHORITY.\n(中文对照:哑光表面(旧木/纸/丝/锦/铜/漆/陶)、细颗粒(与全片一致)。)\n",
			want: "STYLE AUTHORITY.\n",
		},
		{
			// 若把 `[^\n]*` 写成跨行，会把下一段的英文整段吃进去 —— 这条是那条边界的反证。
			name: "不许跨行吃掉后面的英文",
			in:   "(中文对照:本镜是特写。)\n[Shot 1] a close-up of the ledger.\n",
			want: "[Shot 1] a close-up of the ledger.\n",
		},
		{
			// 汉字合法的两个位置一个字都不许动：`<d>` 里的台词、以及主体名。
			name: "`<d>` 台词与主体名一个字都不动",
			in: "沈砚 (S1) says: <d>[Chinese]读我的钢印。</d>\n" +
				"(中文对照:这一句只念一遍。)\n",
			want:    "沈砚 (S1) says: <d>[Chinese]读我的钢印。</d>\n",
			keepCJK: true,
		},
		{
			name: "没有对照块时原样返回（幂等的前提）",
			in:   "Cinematic, 3D CG. [Shot 1] the corridor stands empty.\n",
			want: "Cinematic, 3D CG. [Shot 1] the corridor stands empty.\n",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := StripCNMirror(c.in)
			if got != c.want {
				t.Errorf("StripCNMirror 结果不对\n got=%q\nwant=%q", got, c.want)
			}
			// 幂等：装配点会被调用两次（渲染 + 2K/重绘/音频驱动四路共用）。
			if again := StripCNMirror(got); again != got {
				t.Errorf("不幂等：第二次调用变了\nfirst=%q\nsecond=%q", got, again)
			}
			// 判据：清完之后，**除了 `<d>` 里那句台词与主体名**，正文里不该再有汉字。
			if !c.keepCJK {
				if n := CountCJK(got); n != 0 {
					t.Errorf("清完还留着 %d 个汉字（%q）—— 对照块一个字都不该进提交版", n, got)
				}
			}
		})
	}
}

// TestStripCNMirrorIsTheOnlyMirrorJudge 对照块的**标记词只有一处出处**。
//
// 判据若在两边各写一份（一份在正则里、一份在别处），改了正则忘了标记词就会出现
// 「早退不触发 ⇒ 正则仍跑」或反过来；这里钉住 `cnMirrorWord` 就是那唯一一份。
func TestStripCNMirrorIsTheOnlyMirrorJudge(t *testing.T) {
	if cnMirrorWord != "中文对照" {
		t.Fatalf("标记词被改了（%q）—— 正则与早退都挂在它上面，改它等于换口径", cnMirrorWord)
	}
	if !reCNMirror.MatchString("(中文对照:随便什么)") {
		t.Error("正则认不出最基本的对照块")
	}
	// 反面：只有汉字、没有「中文对照」这个词 —— 那不是对照块，不许被这条正则吃掉
	// （例如角色卡原文里混进来的中文注释，归 StripCJK 管）。
	if reCNMirror.MatchString("这是角色卡里混进来的一句中文注释。") {
		t.Error("正则把普通中文也当成对照块了 —— 那会连卡原文一起删掉")
	}
	// 反面之二：正则必须吃掉整块（行内最后一个右括号），不能停在第一个右括号。
	got := StripCNMirror("X.\n(中文对照:哑光(旧木/铜)、细颗粒。)\n")
	if got != "X.\n" {
		t.Errorf("对照块带内层括号时没被整块吃掉，残留 %q", got)
	}
}
