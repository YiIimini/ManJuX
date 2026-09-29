package server

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/YiIimini/ManJuX/internal/project"
)

// 本分区前端改动的接线闸门(甲1/甲3 的界面接线、乙5 的忙提示、乙7 的 2K 导出、乙9 的云端 seed 标记)。
//
// 为什么把前端的闸门写在 server 包里:这不是"顺手",是这个仓库既有的做法 ——
// 前端没有单元测试运行器,而**本包 embed 并对外提供前端产物**(webdist),
// 于是"前端源码里到底有没有把某个字段接上"这件事,只有在这里才有人跑
// (见 frontend_routes_test.go / frontend_guard_test.go / theme_contract_test.go)。
//
// 每条都按"漏接线的**症状**"来写判据,而不是"某个字符串在不在":
//   - 新参数没接上 → 控件是假开关(能点、能存、后端不认);
//   - 忙提示没排在提交前 → 提示弹在提交之后,等于没提示;
//   - 口径写了两份 → 两份会漂移,同一个问题看起来像两个问题。

// 前端文件的读取走 frontend_novel_detail_test.go 里已有的 readFrontend
// (rel 相对 frontend/ 目录,所以这里一律以 "src/" 开头)。

// frontendSources 遍历 frontend/src 下的全部 .vue/.js(文件名为相对 frontend/src)。
func frontendSources(t *testing.T) map[string]string {
	t.Helper()
	root := filepath.Join("..", "..", "frontend", "src")
	out := map[string]string{}
	err := filepath.Walk(root, func(p string, info os.FileInfo, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if info.IsDir() {
			return nil
		}
		switch strings.ToLower(filepath.Ext(p)) {
		case ".vue", ".js":
		default:
			return nil
		}
		raw, rerr := os.ReadFile(p)
		if rerr != nil {
			return rerr
		}
		rel, _ := filepath.Rel(root, p)
		out[filepath.ToSlash(rel)] = string(raw)
		return nil
	})
	if err != nil {
		t.Fatalf("扫描 frontend/src 失败:%v", err)
	}
	if len(out) == 0 {
		t.Fatal("没扫到任何前端文件 —— 闸门失效")
	}
	return out
}

// uniqueOwner 断言某段文案只存在于一个文件里。
//
// 为什么值得单独一条判据:同一句话抄成几份,改的时候只会改到自己看到的那一份 ——
// 而"这个引擎做不到这件事"的说法一旦不一致,用户会以为遇到的是两个不同的问题。
func uniqueOwner(t *testing.T, all map[string]string, snippet, wantOwner string) {
	t.Helper()
	var owners []string
	for rel, src := range all {
		if strings.Contains(src, snippet) {
			owners = append(owners, rel)
		}
	}
	if len(owners) != 1 || owners[0] != wantOwner {
		t.Errorf("文案「%s」必须只存在于 %s 一处,实际出现在:%v", snippet, wantOwner, owners)
	}
}

// TestFrontendParamsDrawerWiresGradeAndSeamReset 甲1(调色档)与甲3(接缝重置间隔)
// 必须真的接进项目参数抽屉。
//
// 不接线的后果是**假开关**:控件在界面上能动、保存也回 200,而参数根本没进项目快照
// (或者根本读不回来),用户以为设了,渲染照旧。所以判据落在三件事上:
//   - 值绑在项目的哪一层(assemble.grade / render.seam_reset_every);
//   - 缺键时的归一化(存量项目快照里没有这两个键 —— 不做归一化,下拉空白,
//     而且**保存是整份快照回写**,空值会被写进项目参数);
//   - 档位表与后端词汇表一致(off/soft/film/heavy,默认 film)。
func TestFrontendParamsDrawerWiresGradeAndSeamReset(t *testing.T) {
	drawer := readFrontend(t, "src/components/ParamsDrawer.vue")
	hints := readFrontend(t, "src/renderHints.js")

	// ① 档位表:四个档位一个不能少,默认档是 film
	for _, key := range []string{"off", "soft", "film", "heavy"} {
		if !regexp.MustCompile(`key:\s*'` + key + `'`).MatchString(hints) {
			t.Errorf("调色档缺少 %q —— 后端有这一档而界面上选不到,等于该档不可用", key)
		}
	}
	if !regexp.MustCompile(`DEFAULT_GRADE\s*=\s*'film'`).MatchString(hints) {
		t.Error("默认调色档必须是 film(与后端默认一致);默认档不一致会让存量项目在保存后悄悄换档")
	}
	for _, want := range []string{"冷暖分离", "颗粒", "暗角"} {
		if !strings.Contains(hints, want) {
			t.Errorf("调色档的中文说明必须提到「%s」—— 用户要能看出这一档到底做了什么", want)
		}
	}

	// ② 抽屉里真的绑上了这两个字段(v-model 打在正确的参数路径上)
	if !strings.Contains(drawer, `v-model="form.assemble.grade"`) {
		t.Error("项目参数抽屉没有绑定 form.assemble.grade —— 调色档是假开关")
	}
	if !strings.Contains(drawer, `v-model="form.render.seam_reset_every"`) {
		t.Error("项目参数抽屉没有绑定 form.render.seam_reset_every —— 接缝重置间隔是假开关")
	}
	if !strings.Contains(drawer, "GRADE_OPTIONS") {
		t.Error("档位清单必须来自 renderHints.js(单一真源),不能在抽屉里另写一份")
	}

	// ③ 缺键归一化:老项目快照里没有这两个键,必须在打开抽屉时兜底,
	//    否则下拉空白 + 保存回写空值(看起来像「界面坏了」,后端还得再兜一次)
	for _, want := range []string{"form.assemble.grade = gradeOf(", "form.render.seam_reset_every = seamResetOf("} {
		if !strings.Contains(drawer, want) {
			t.Errorf("抽屉打开时缺少缺键兜底:%s", want)
		}
	}
	// 归一化函数本身要能吃掉脏值(空字符串 / 负数 / 垃圾)
	if !strings.Contains(hints, "GRADE_KEYS.includes(v) ? v : DEFAULT_GRADE") {
		t.Error("gradeOf 必须把认不出的档位(缺键/手改 JSON)回退到默认档,而不是原样透传")
	}
	if !strings.Contains(hints, "n < 0) return 0") {
		t.Error("seamResetOf 必须把负数/脏值回退到 0(0 = 只在换场/换人时断开,最保守的选择)")
	}

	// ③b 「自动」档必须可选,且缺键时要落在它上面(技能侧清单 §2.1)。
	//
	// 为什么是闸门而不是"顺手加的选项":自动降档(动画电影 → soft)要在**未表态**时生效,
	// 而"未表态"必须能被这个下拉表达出来 —— 缺键若兜成显式 film,那么开一次参数抽屉
	// (哪怕只是改字幕字号)再保存就把它钉死成 film,自动降档静默失效,用户什么都没改。
	if !regexp.MustCompile(`key:\s*'auto'`).MatchString(hints) {
		t.Error("调色档缺少 auto(自动)档 —— 缺键只能兜成 film,按风格自动降档会被一次保存悄悄废掉")
	}
	if !strings.Contains(hints, "if (v === '') return AUTO_GRADE") {
		t.Error("gradeOf 必须把缺键/空串归成 auto(未表态),而不是兜成 film")
	}

	// ④ 说明文案:接缝重置必须说清「换场/换人会自动断开」与「0/1 = 每场重置」
	if !strings.Contains(hints, "换场 / 换人时自动断开接缝") && !strings.Contains(drawer, "换场 / 换人会自动断开接缝") {
		t.Error("接缝重置间隔必须写明「换场/换人会自动断开接缝」—— 否则用户会以为 0 就是完全不重置")
	}
	if !strings.Contains(drawer, "每场重置") {
		t.Error("接缝重置间隔的标签要写清 0/1 = 每场重置的语义")
	}
}

// TestFrontendParamsDrawerWiresDeliveryAndSubtitleMargin 交付分辨率(P1-1)与字幕安全区(P0-3)
// 必须真的接进项目参数抽屉,且默认口径与后端一致。
//
// 为什么这两条要闸门:它们改的是**交付口**(成片最终画幅 + 字幕落点),用户在界面上
// 看不出"没接上" —— 参数照存、成片照出,只是没按用户以为的口径出。判据落三件事:
//   - 值绑在 assemble.deliver_width/deliver_height/subtitle_margin_v;
//   - 缺键时有归一化(老项目快照没有这些键,保存是整份快照回写);
//   - 默认口径与后端同一个数(前端 15% ↔ 后端 Sanitize 落 15;上限 30 两边一致)。
func TestFrontendParamsDrawerWiresDeliveryAndSubtitleMargin(t *testing.T) {
	drawer := readFrontend(t, "src/components/ParamsDrawer.vue")
	hints := readFrontend(t, "src/renderHints.js")

	for _, want := range []string{
		`v-model="form.assemble.deliver_width"`,
		`v-model="form.assemble.deliver_height"`,
		`v-model="form.assemble.subtitle_margin_v"`,
	} {
		if !strings.Contains(drawer, want) {
			t.Errorf("项目参数抽屉缺少 %s —— 这个参数是假开关(能存、后端不认或用户设不到)", want)
		}
	}
	for _, want := range []string{
		"form.assemble.deliver_width = deliverEdgeOf(",
		"form.assemble.deliver_height = deliverEdgeOf(",
		"form.assemble.subtitle_margin_v = subtitleMarginOf(",
	} {
		if !strings.Contains(drawer, want) {
			t.Errorf("抽屉打开时缺少缺键兜底:%s(否则空值会被整份快照回写进项目参数)", want)
		}
	}
	// 交付档的"未表态 = 按画幅自动"必须能被界面表达出来,且不能被前端就地算成具体尺寸
	// (按画幅算只有一个实现点:后端 compose.deliverSize)
	if !strings.Contains(hints, "export function deliverEdgeOf") || !strings.Contains(hints, "if (!Number.isFinite(v) || v <= 0) return 0") {
		t.Error("deliverEdgeOf 必须把未填/脏值保留成 0(= 按画幅自动),不能在前端替后端算出 1080")
	}
	if !strings.Contains(drawer, "deliverSummary(") {
		t.Error("交付分辨率必须有人话说明(留 0 = 按画幅自动这条语义不看说明是猜不出来的)")
	}

	// 默认口径两侧同一个数:前端 15%(安全区下限)↔ 后端 Sanitize 的同一落点
	if !strings.Contains(hints, "SUBTITLE_MARGIN_DEFAULT = 15") {
		t.Error("字幕下边距的默认值必须是 15%(避开平台底部 UI 安全区)—— 旧值 8% 正压在危险区里")
	}
	var p project.StageParams
	p.Sanitize()
	if p.Assemble.SubtitleMarginV != 15 {
		t.Errorf("后端 Sanitize 的下边距默认值是 %d,而前端说明写的是 15 —— 两侧必须是同一个数",
			p.Assemble.SubtitleMarginV)
	}
	if !strings.Contains(hints, "SUBTITLE_MARGIN_MAX = 30") || project.DeliverMaxEdge != 8192 {
		t.Error("字幕下边距上限(30)与交付边长上限(8192)必须两侧同口径,否则界面能填出后端会钳掉的值")
	}
}

// TestFrontend2KExportEntry 乙7:单镜云端 2K 产物在界面上要「列得出、看得见、拿得走」,
// 并且**必须写明它不进成片**。
//
// 最后那句是本条的重点:管线侧已经会在跑完的日志里如实说「这是独立导出文件」,
// 但那是跑完**之后**;界面入口若只列文件不做说明,用户点开就会以为自己把成片升到了 2K ——
// 而合成只收 manifest.File,那一镜仍是 768p。
func TestFrontend2KExportEntry(t *testing.T) {
	store := readFrontend(t, "src/store.js")
	view := readFrontend(t, "src/views/ProjectView.vue")

	// ① api 层两条:列表走 req(会被 TestFrontendURLsMatchBackendRoutes 对上路由表),
	//    URL 拼装走独立方法(取回路由的名字形状由后端白名单校验)
	if !strings.Contains(store, "/outputs/2k") {
		t.Error("store.js 缺少 2K 产物的列表请求 —— 入口没有数据来源")
	}
	if !strings.Contains(store, "output2kURL:") {
		t.Error("store.js 缺少 2K 产物的 URL 拼装方法(取回路由 /outputs/2k/{name})")
	}
	if !strings.Contains(store, "encodeURIComponent(name)") {
		t.Error("取回 URL 的文件名必须编码 —— 名字来自后端列表,不编码会在特殊字符上拼出坏 URL")
	}

	// ② 视图里真的用了这两个方法(而不是只定义在 api 层没人调)
	for _, want := range []string{"api.outputs2k(", "api.output2kURL("} {
		if !strings.Contains(view, want) {
			t.Errorf("ProjectView 没有调用 %s —— 入口没接线", want)
		}
	}

	// ③ 口径:必须明确写出四个点。缺任何一个,用户都可能误以为 2K 导出 = 成片升级。
	for _, want := range []string{"独立导出文件", "不进镜头清单", "不参与合成", "768p"} {
		if !strings.Contains(view, want) {
			t.Errorf("2K 导出入口必须写清「%s」—— 只说「2K 导出」会让人以为成片跟着变 2K", want)
		}
	}
}

// TestFrontendGpuContentionGuard 乙5 的温和版:局部重绘 / 音频驱动 / 单资产重生成
// 在**提交之前**要检查管线是否在跑,在跑就弹一次确认(不禁用)。
//
// 判据刻意是**顺序**而不是「有没有这一行」:提示排在提交之后等于没提示
// (用户点完才发现,GPU 已经抢上了)。所以每个入口都断言
// `confirmGpuContention(` 出现在**同一个处理函数体内**、且在该次 api 调用之前。
func TestFrontendGpuContentionGuard(t *testing.T) {
	busy := readFrontend(t, "src/pipelineBusy.js")

	// ① 提示口径:三件事缺一不可(正在渲染 / 会互相拖慢 / 显存与算力在抢)
	for _, want := range []string{"管线正在渲染", "互相拖慢", "显存"} {
		if !strings.Contains(busy, want) {
			t.Errorf("忙提示必须包含「%s」—— 只说「确定继续?」用户不知道为什么该犹豫", want)
		}
	}
	// ② 判据必须现读(不是吃 15s 轮询的缓存),并且不把「读失败」当成忙
	if !strings.Contains(busy, "api.status()") {
		t.Error("忙判据必须提交前现读 /api/status —— 拿 15s 前的快照判断会得出反的结论")
	}
	if !strings.Contains(busy, "return Object.keys(G.status?.running || {}).length > 0") {
		t.Error("读状态失败时不能当成「忙」:拿失败的网络请求挡住用户点击,比漏一次提示更糟")
	}
	// ③ 温和版:确认后照常提交(return true),取消才不提交(return false)
	if !strings.Contains(busy, "return true") || !strings.Contains(busy, "return false") {
		t.Error("确认函数必须做到「确认 → 继续提交,取消 → 不提交」—— 直接 return 会让入口变成禁用")
	}

	cases := []struct {
		file   string
		submit string
		what   string
	}{
		{"src/components/ShotCard.vue", "api.maskFix(", "局部重绘"},
		{"src/components/ShotCard.vue", "api.audioDrive(", "音频驱动"},
		{"src/components/StageDetailDrawer.vue", "api.regenAsset(", "单资产重生成"},
		// 资产库重抽：处置函数随详情弹窗搬进了**共用组件**（2026-09-29：资产库与工作台同一份）
		{"src/components/LibAssetDialog.vue", "api.libraryRegen(", "资产库重抽"},
	}
	for _, c := range cases {
		src := readFrontend(t, c.file)
		submit := strings.Index(src, c.submit)
		if submit < 0 {
			t.Errorf("%s 里找不到提交调用 %s(闸门解析可能失效)", c.file, c.submit)
			continue
		}
		// 取该次提交所在的处理函数体(从最近的 async function 到这次提交)
		head := src[:submit]
		if i := strings.LastIndex(head, "async function"); i >= 0 {
			head = head[i:]
		}
		if !strings.Contains(head, "confirmGpuContention(") {
			t.Errorf("%s(%s)在提交前没有检查管线是否在跑 —— 管线跑着时点它会抢 GPU(两边都变慢)",
				c.file, c.what)
			continue
		}
		if !strings.Contains(head, "!(await confirmGpuContention(") {
			t.Errorf("%s(%s)取到了确认结果却没有用它决定是否提前返回 —— 那只是一句提醒,不是知情选择",
				c.file, c.what)
		}
	}

	// ④ 提示文案只能有一份实现
	uniqueOwner(t, frontendSources(t), "互相拖慢(显存与算力都在抢)", "pipelineBusy.js")
}

// TestFrontendCloudSeedNote 乙9:引擎=云端 且 seed≠0 时,界面必须当场说明
// 「云端不支持 seed、产物不可复现、要复现请切本地 ComfyUI」。
//
// 与后端能力提示(unsupportedParamNote)同一口径:后端那句是**跑起来之后**在日志里说,
// 前端这句是**选引擎/填 seed 时**在界面上说 —— 同一件事的事前版本,
// 因此两处都必须落在「云端不支持 / 不可复现 / 切本地 ComfyUI」这三个点上。
func TestFrontendCloudSeedNote(t *testing.T) {
	hints := readFrontend(t, "src/renderHints.js")

	for _, want := range []string{"云端引擎不支持 seed", "不可复现", "本地 ComfyUI"} {
		if !strings.Contains(hints, want) {
			t.Errorf("云端 seed 提示必须写清「%s」(与后端 unsupportedParamNote 的口径对齐)", want)
		}
	}
	// 触发条件:只认云端引擎名,且 seed≠0
	if !regexp.MustCompile(`CLOUD_ENGINES\s*=\s*\[[^\]]*'api'`).MatchString(hints) {
		t.Error("云端引擎名清单必须包含 'api'(engine.Registry 里的注册名)")
	}
	if !strings.Contains(hints, "if (s === 0) return ''") {
		t.Error("seed=0(每镜随机)本来就不谈复现,不该弹这条提示")
	}
	if !strings.Contains(hints, "if (!CLOUD_ENGINES.includes(eng)) return ''") {
		t.Error("非云端引擎(本地/auto)不该弹这条提示;引擎=auto 时不替它下结论")
	}

	// 两个使用点都必须真的渲染它:项目参数抽屉(引擎 + seed 同屏)与单镜微调弹窗
	// (这个弹窗的意义正是「同参数反复对比」,最容易被云端 seed 失效坑到)
	for _, f := range []string{"src/components/ParamsDrawer.vue", "src/components/ShotTuneDialog.vue"} {
		src := readFrontend(t, f)
		if !strings.Contains(src, "cloudSeedWarning(") {
			t.Errorf("%s 没有按「云端 + 固定 seed」给出提示 —— 用户会以为自己做了受控对比", f)
		}
		if !strings.Contains(src, "v-if=\"cloudSeedNote\"") {
			t.Errorf("%s 算出了提示却没有渲染(模板里必须有 v-if=\"cloudSeedNote\" 的 alert)", f)
		}
	}

	// 文案单点:不允许第二个文件再写一份「云端不支持 seed」
	uniqueOwner(t, frontendSources(t), "云端引擎不支持 seed", "renderHints.js")
}
