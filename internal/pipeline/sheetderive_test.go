package pipeline

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/YiIimini/ManJuX/internal/config"
	"github.com/YiIimini/ManJuX/internal/engine"
	"github.com/YiIimini/ManJuX/internal/ff"
	"github.com/YiIimini/ManJuX/internal/library"
	"github.com/YiIimini/ManJuX/internal/project"
)

// 造型变体的定妆照派生(技能侧清单 §4.1)的契约测试。
//
// 守的是那件"用户一眼就能看出来"的事:同一个角色的几张造型卡必须还是同一张脸。
// 每张卡各自文生图 → 脸会漂(refmanifest.go 注释里记着的"同一角色逐镜不同人");
// 而从主卡派生 + 身份校验,这条链上的每一步都必须真的接上 —— 判据错、降级静默、
// 校验空转,三种缺陷都不会报错,只会表现成"这一档长得不像他"。

// ---------- 纯判据 ----------

func TestSheetVariantTrigger(t *testing.T) {
	cases := []struct {
		name    string
		id, nm  string
		ek      EntityKind
		hasBase bool
		want    bool
	}{
		{"主卡(id 无档位、名字无档名)→ 不派生", "char_01", "翻篇", KindHuman, false, false},
		{"变体卡(id vK)→ 派生", "char_01v4", "翻篇·归零", KindSpiritPet, true, true},
		{"变体卡(名字带档名 + 方案里确有基名卡)→ 派生", "abc", "翻篇·归零", KindHuman, true, true},
		// 2026-09-26 真机事故:《求你们骂我》的正卡名就叫「沈砚·一品」,id 仍是 char_01、
		// 方案里也没有另一张"沈砚" —— 初版只凭"名字里有中点"就判成造型档,于是它去找一个
		// 不存在的主卡,每次定妆都播报"主卡缺图,退回文生图"(而那张图一直是对的)。
		{"正卡名里带中点但方案里没有基名卡 → **不派生**(不再误报主卡缺图)", "char_01", "沈砚·一品", KindHuman, false, false},
		// 【2026-09-28 用户口径反转】「Q 版直接使用对应角色定妆照图片生图片逻辑才对」——
		// Q 版是同一个人的另一种画法，定妆照才是身份唯一权威；走文生图 = 把长相重新描述一遍
		// ⇒ 必然得到"像但不完全像"的第二张脸。所以 Q 版**要**走派生（且用自己那套派生指令）。
		{"Q 版资产(名字「X · Q版」+ 方案里有 X)→ 派生", "char_01_chibi", "青禾 · Q版", KindChibi, true, true},
		// 但**没有对应角色时不许派生**：系统小人（chibi_sys_01，方案里没有"系统"这个人）
		// 找不到主卡，硬派生只会每次都播报"主卡缺图"。
		{"Q 版资产但没有对应的主卡 → 不派生", "chibi_sys_01", "系统机器人", KindChibi, false, false},
		{"普通角色(id 数字尾但不是 vK)→ 不派生", "char_02", "小喇叭", KindSpiritPet, false, false},
		{"空卡 → 不派生", "", "", KindHuman, false, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := sheetVariantTrigger(tc.id, tc.nm, tc.ek, tc.hasBase); got != tc.want {
				t.Fatalf("sheetVariantTrigger(%q,%q,%s,hasBase=%v) = %v,期望 %v", tc.id, tc.nm, tc.ek, tc.hasBase, got, tc.want)
			}
		})
	}
}

func TestChooseSheetRoute(t *testing.T) {
	cases := []struct {
		name              string
		editReady, i2iCap bool
		want              sheetDeriveRoute
	}{
		{"编辑就绪 → 编辑(身份按像素保住,每张数秒)", true, true, routeEdit},
		{"编辑就绪、引擎不支持图生图 → 仍走编辑", true, false, routeEdit},
		{"编辑没就绪、支持图生图 → 图生图", false, true, routeImg2Img},
		{"两条都没有 → 文生图(调用方必须播报降级)", false, false, routeText2Img},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := chooseSheetRoute(tc.editReady, tc.i2iCap); got != tc.want {
				t.Fatalf("chooseSheetRoute(%v,%v) = %v,期望 %v", tc.editReady, tc.i2iCap, got, tc.want)
			}
		})
	}
}

// 派生指令必须在**最前面**(身份/换装指令在前,风格与取景在后):定妆提示词里的取景
// 与介质块也带"照抄参考图"这类措辞,排在前面会把"只改服装"这一条淹没。
func TestSheetDerivePromptOrderAndIdempotence(t *testing.T) {
	sheet := "A round yellow dog ..., plain background, upper-body framing, front-facing, RENDER STYLE: cel"
	got := SheetDerivePrompt(sheet)
	if !strings.HasPrefix(got, SheetDeriveMarker) {
		t.Fatalf("派生指令必须排在提示词最前面,实际开头是:%s", firstRunes(got, 60))
	}
	if !strings.Contains(got, "Keep the face, muzzle shape, fur colour and markings EXACTLY as the reference") {
		t.Errorf("必须逐字带上技能侧给的换装指令原句:%s", got)
	}
	if !strings.HasSuffix(strings.TrimSpace(got), strings.TrimSpace(sheet)) {
		t.Errorf("原定妆提示词必须原样保留在后面(取景与风格不能丢):%s", got)
	}
	if again := SheetDerivePrompt(got); again != got {
		t.Errorf("重复装配必须幂等(否则反复生成会把指令叠成一堆):\n %s", again)
	}
}

// denoise 常量必须在"保身份"的那一档里:高了身份漂移、低了服装换不过来。
// 这条不是形式主义 —— 该常量是全平台唯一一处,被人顺手调大就会让所有造型档开始换脸。
func TestSheetDeriveDenoiseStaysInIdentityBand(t *testing.T) {
	if SheetDeriveDenoise < 0.45 || SheetDeriveDenoise > 0.65 {
		t.Fatalf("SheetDeriveDenoise=%.2f 落在实测的保身份区间(0.6–0.75)之外太多:"+
			"低于 0.45 服装几乎换不过来,高于 0.65 开始换脸(见 imagegen.go 的实测记录与常量注释)",
			SheetDeriveDenoise)
	}
}

func firstRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}

// ---------- 家族主卡的选择 ----------

func TestFamilyMasterInAssets(t *testing.T) {
	assets := map[string]project.Asset{
		"char:char_01v1": {ID: "char:char_01v1", Kind: "characters", Name: "翻篇·收容所", File: "assets/characters/a.png"},
		"char:char_01v2": {ID: "char:char_01v2", Kind: "characters", Name: "翻篇·进城", File: "assets/characters/b.png"},
		"char:char_01v4": {ID: "char:char_01v4", Kind: "characters", Name: "翻篇·归零", File: "assets/characters/d.png"},
		"char:char_02":   {ID: "char:char_02", Kind: "characters", Name: "小喇叭", File: "assets/characters/e.png"},
		"scene:scene_01": {ID: "scene:scene_01", Kind: "scenes", Name: "临水城", File: "assets/scenes/s.png"},
	}
	// 第 4 档要拿第 1 档当起点(档位号最小),而不是自己、也不是别的角色
	a, ok := familyMasterInAssets(assets, "char:char_01v4", "char_1", "翻篇")
	if !ok || a.Name != "翻篇·收容所" {
		t.Fatalf("应挑同族档位最靠前的卡,实际 ok=%v name=%q", ok, a.Name)
	}
	// 名字口径也要认(id 口径对不上时,靠名字基名仍能认出一家人)
	if a, ok := familyMasterInAssets(assets, "char:char_99", "char_99", "翻篇"); !ok || a.Name != "翻篇·收容所" {
		t.Errorf("id 口径对不上时应按名字口径认出一家人,实际 ok=%v name=%q", ok, a.Name)
	}
	// 场景不算家族成员(名字基名一模一样也不许被当成主卡)
	sceneNamed := map[string]project.Asset{
		"scene:scene_01": {ID: "scene:scene_01", Kind: "scenes", Name: "翻篇", File: "assets/scenes/s.png"},
	}
	if _, ok := familyMasterInAssets(sceneNamed, "char:char_01v4", "char_1", "翻篇"); ok {
		t.Error("场景资产不是角色,不该被当成派生起点")
	}
	// 同族里只有自己 → 没有可用起点
	only := map[string]project.Asset{
		"char:char_01v4": {ID: "char:char_01v4", Kind: "characters", Name: "翻篇·归零", File: "assets/characters/d.png"},
	}
	if _, ok := familyMasterInAssets(only, "char:char_01v4", "char_1", "翻篇"); ok {
		t.Error("排除自己之后没有同族卡时,必须老实返回未命中(调用方据此降级并播报)")
	}
}

// 主卡必须排在变体卡**之前**生成,否则本集刚出的主卡图还没落盘,变体只能退回文生图。
func TestOrderCharsMasterFirst(t *testing.T) {
	in := []project.Character{
		{ID: "char_01v4", Name: "翻篇·归零"},
		{ID: "char_02", Name: "小喇叭"},
		{ID: "char_01v1", Name: "翻篇·收容所"},
		{ID: "char_01v2", Name: "翻篇·进城"},
	}
	out := orderCharsMasterFirst(in)
	got := make([]string, len(out))
	for i, ch := range out {
		got[i] = ch.ID
	}
	want := []string{"char_01v1", "char_01v2", "char_01v4", "char_02"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("同族必须挨着且主卡在前(档位升序),实际 %v,期望 %v", got, want)
	}
	// 组间保持首次出现的顺序(不改写"谁先谁后"的观感)
	if len(orderCharsMasterFirst(nil)) != 0 {
		t.Error("空清单应返回空")
	}
	// 认不出家族的卡各自成组,不许被并到一起
	anon := orderCharsMasterFirst([]project.Character{{Name: "甲"}, {Name: "乙"}})
	if len(anon) != 2 || anon[0].Name != "甲" || anon[1].Name != "乙" {
		t.Errorf("无名无 id 的卡应保持原序,实际 %v", anon)
	}
}

// ---------- 假引擎 ----------

// fakeSheetEngine 只做两件事:记录"走了哪个入口",并落一张真 PNG(下游要读得到文件)。
type fakeSheetEngine struct {
	t2i, i2i    int
	lastRef     string
	lastPrompt  string
	lastDenoise float64
	lastSeed    int64
}

func (f *fakeSheetEngine) Name() string                    { return "comfy" }
func (f *fakeSheetEngine) Available(context.Context) error { return nil }
func (f *fakeSheetEngine) FreeVRAM(context.Context) error  { return nil }
func (f *fakeSheetEngine) ClearQueue(context.Context)      {}
func (f *fakeSheetEngine) Interrupt(context.Context)       {}
func (f *fakeSheetEngine) Render(context.Context, engine.Job) (*engine.Result, error) {
	return nil, fmt.Errorf("本用例不该走视频渲染")
}

// GenerateMusic 桩：本测试不涉及配乐生成（与云端引擎同一政策：安全空实现）。
func (f *fakeSheetEngine) GenerateMusic(context.Context, engine.MusicJob) (string, error) {
	return "", nil
}

func (f *fakeSheetEngine) Upscale(context.Context, string, engine.UpscaleOptions, int, float64, string, string, time.Duration) (string, error) {
	return "", fmt.Errorf("本用例不该走放大")
}
func (f *fakeSheetEngine) GenerateImage(_ context.Context, prompt, _ string, seed int64, w, h int, outPath string) (string, error) {
	f.t2i++
	f.lastPrompt, f.lastSeed = prompt, seed
	return touchPNG(outPath, w, h)
}
func (f *fakeSheetEngine) GenerateImageFrom(_ context.Context, ref, prompt, _ string, seed int64, w, h int, denoise float64, outPath string) (string, error) {
	f.i2i++
	f.lastRef, f.lastPrompt, f.lastDenoise, f.lastSeed = ref, prompt, denoise, seed
	return touchPNG(outPath, w, h)
}

func touchPNG(path string, w, h int) (string, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return "", err
	}
	f, err := os.Create(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	if _, err := f.Write([]byte("png-bytes")); err != nil {
		return "", err
	}
	return path, nil
}

// ---------- 测试台 ----------

type sheetTestRig struct {
	c     *Coordinator
	store *project.Store
	proj  *project.Project
	lib   *library.Library
	bus   *Bus
}

func newSheetRig(t *testing.T) *sheetTestRig {
	t.Helper()
	root := t.TempDir()
	lib, err := library.New(filepath.Join(root, "library"))
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := config.NewManager(filepath.Join(root, "settings.json"), root)
	if err != nil {
		t.Fatal(err)
	}
	store, err := project.NewStore(filepath.Join(root, "projects"))
	if err != nil {
		t.Fatal(err)
	}
	proj, err := store.Create("造型档测试", project.ProjectConfig{Params: project.DefaultStageParams()})
	if err != nil {
		t.Fatal(err)
	}
	bus := NewBus()
	return &sheetTestRig{c: New(store, cfg, engine.NewRegistry(), new(ff.FF), bus, lib), store: store, proj: proj, lib: lib, bus: bus}
}

// masterSheet 在项目里放一张"主卡已生成"的定妆照,返回其绝对路径。
func (r *sheetTestRig) masterSheet(t *testing.T, id, name string) (string, map[string]project.Asset) {
	t.Helper()
	rel := "assets/characters/" + id + ".png"
	full := filepath.Join(r.store.Dir(r.proj.ID), rel)
	writeTestPNG(t, full, 64, 96)
	assets := map[string]project.Asset{
		"char:" + id: {ID: "char:" + id, Kind: "characters", Name: name, File: filepath.ToSlash(rel)},
	}
	if err := r.store.SaveAssets(r.proj.ID, assets); err != nil {
		t.Fatal(err)
	}
	return full, assets
}

func (r *sheetTestRig) messages() []string {
	evs, _ := r.bus.Recent(0)
	out := make([]string, 0, len(evs))
	for _, e := range evs {
		out = append(out, e.Level+"|"+e.Message)
	}
	return out
}

func (r *sheetTestRig) findMessage(sub ...string) string {
	for _, m := range r.messages() {
		ok := true
		for _, s := range sub {
			if !strings.Contains(m, s) {
				ok = false
				break
			}
		}
		if ok {
			return m
		}
	}
	return ""
}

// varyVLM 造一个假视觉模型:按顺序给出判定结果(末条重复使用)。
func varyVLM(t *testing.T, verdicts ...string) *httptest.Server {
	t.Helper()
	i := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		v := verdicts[len(verdicts)-1]
		if i < len(verdicts) {
			v = verdicts[i]
		}
		i++
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprintf(w, `{"choices":[{"message":{"role":"assistant","content":%q},"finish_reason":"stop"}]}`, v)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func (r *sheetTestRig) useVLM(t *testing.T, url string) {
	t.Helper()
	s := r.c.cfg.Get()
	s.VLM = config.VLMSettings{BaseURL: url, APIKey: "k", Model: "fake-vlm"}
	if err := r.c.cfg.Update(s); err != nil {
		t.Fatal(err)
	}
}

const identityPass = `{"frames":[{"index":1,"same_person":true,"score":93,"issue":""}],"min_score":70,"pass":true,"reason":"同一个"}`

const identityFail = `{"frames":[{"index":1,"same_person":false,"score":21,"issue":"毛色与印记都变了"}],"min_score":70,"pass":false,"reason":"不是同一只"}`

// ---------- 出图路线:主卡文生图 / 变体卡派生 ----------

func TestGenSheetImageRoutes(t *testing.T) {
	rig := newSheetRig(t)
	eng := &fakeSheetEngine{}
	base := sheetJob{proj: rig.proj, kind: "characters", name: "翻篇", prompt: "A round yellow dog", ek: KindSpiritPet, w: 768, h: 1344, seed: 7}

	// ① 主卡:**没有**更早的参考可依 → 文生图(不许拿别的角色当参考)
	master := base
	master.id, master.assets = "char_01v1", map[string]project.Asset{}
	if _, err := rig.c.genSheetImage(context.Background(), eng, master); err != nil {
		t.Fatal(err)
	}
	if eng.t2i != 1 || eng.i2i != 0 {
		t.Fatalf("主卡必须走文生图,实际 t2i=%d i2i=%d", eng.t2i, eng.i2i)
	}

	// ② 变体卡 + 同族主卡图在项目里 → 图生图(以主卡为 --init-image)
	masterPath, assets := rig.masterSheet(t, "char_01v1", "翻篇·收容所")
	variant := base
	variant.id, variant.name, variant.assets = "char_01v4", "翻篇·归零", assets
	variant.prompt = "Sad state: the fur is dull and the brass tag is gone. plain background, upper-body framing"
	if _, err := rig.c.genSheetImage(context.Background(), eng, variant); err != nil {
		t.Fatal(err)
	}
	if eng.i2i != 1 || eng.t2i != 1 {
		t.Fatalf("变体卡必须走图生图派生,实际 t2i=%d i2i=%d", eng.t2i, eng.i2i)
	}
	if eng.lastRef != masterPath {
		t.Errorf("派生的参考图必须是同族主卡那张,实际 %q(期望 %q)", eng.lastRef, masterPath)
	}
	if eng.lastDenoise != SheetDeriveDenoise {
		t.Errorf("去噪强度必须用常量 %.2f,实际 %.2f", SheetDeriveDenoise, eng.lastDenoise)
	}
	if !strings.HasPrefix(eng.lastPrompt, SheetDeriveMarker) {
		t.Errorf("派生指令必须在提示词最前,实际开头:%s", firstRunes(eng.lastPrompt, 50))
	}
	if !strings.Contains(eng.lastPrompt, "brass tag is gone") {
		t.Errorf("本卡自己的状态描述不能丢:%s", eng.lastPrompt)
	}
}

// 主卡缺图时必须**如实降级并播报**,新版图落盘照旧(不许静默退回文生图)。
func TestGenSheetImageDegradesWhenMasterMissing(t *testing.T) {
	rig := newSheetRig(t)
	eng := &fakeSheetEngine{}
	j := sheetJob{
		proj: rig.proj, assets: map[string]project.Asset{}, kind: "characters",
		id: "char_01v4", name: "翻篇·归零", prompt: "state", ek: KindSpiritPet, w: 768, h: 1344, seed: 7,
	}
	path, err := rig.c.genSheetImage(context.Background(), eng, j)
	if err != nil {
		t.Fatal(err)
	}
	if path == "" {
		t.Fatal("降级也要出图(资产阶段不能因为缺主卡就停摆)")
	}
	if eng.t2i != 1 || eng.i2i != 0 {
		t.Fatalf("找不到主卡时必须退回文生图,实际 t2i=%d i2i=%d", eng.t2i, eng.i2i)
	}
	msg := rig.findMessage("主卡缺图", "翻篇·归零")
	if msg == "" {
		t.Fatalf("降级必须播报(静默的后果是「换了一集就换了一张脸」没人知道),实际事件:%v", rig.messages())
	}
	if !strings.Contains(msg, "退回文生图") || !strings.Contains(msg, "相似度不保证") {
		t.Errorf("播报要把后果说清(退回文生图 + 相似度不保证),实际:%s", msg)
	}
	if !strings.HasPrefix(msg, "warn|") {
		t.Errorf("这是需要用户知道的事,应记为 warn:%s", msg)
	}
}

// Q 版资产**要走派生**：拿对应角色的定妆照做图生图。
//
// 【2026-09-28 用户口径】「Q 版直接使用对应角色定妆照图片生图片逻辑才对」——
// 原实现把它挡在派生之外（"拿真人定妆照派生 Q 版是错的"），结果 Q 版走文生图 = 把长相
// 重新描述一遍 ⇒ "像但不完全像"的第二张脸。这条测试的期望因此**反转**。
func TestGenSheetImageDerivesChibiFromMaster(t *testing.T) {
	rig := newSheetRig(t)
	eng := &fakeSheetEngine{}
	rig.masterSheet(t, "char_01", "青禾")
	// 方案里要有那张主卡 —— `hasBaseCard` 就是靠它确认"确实存在同名的基名卡"
	// （只有资产表里有图、方案里却没这个人，按平台的判据算"有中点 ≠ 有主卡"）。
	if err := rig.store.SavePlan(rig.proj.ID, &project.Plan{
		Characters: []project.Character{{ID: "char_01", Name: "青禾"}},
	}); err != nil {
		t.Fatal(err)
	}
	j := sheetJob{
		proj: rig.proj, assets: mustLoadAssets(t, rig), kind: "characters",
		id: "char_01_chibi", name: "青禾 · Q版", prompt: "chibi", ek: KindChibi, w: 1024, h: 1024, seed: 7,
	}
	if _, err := rig.c.genSheetImage(context.Background(), eng, j); err != nil {
		t.Fatal(err)
	}
	if eng.i2i+eng.t2i == 0 {
		t.Fatal("Q 版形象必须真的出图（派生或降级文生图都算出了，一次都不出才是 bug）")
	}
	if eng.t2i == 1 && eng.i2i == 0 {
		t.Errorf("Q 版必须走**从定妆照派生**（图生图），不该退回文生图 —— "+
			"重新描述一遍长相必然得到「像但不完全像」的第二张脸。实际 t2i=%d i2i=%d", eng.t2i, eng.i2i)
	}
	if m := rig.findMessage("主卡缺图"); m != "" {
		t.Errorf("主卡在图、方案里也有对应角色，不该有「主卡缺图」的降级播报:%s", m)
	}
}

func mustLoadAssets(t *testing.T, rig *sheetTestRig) map[string]project.Asset {
	t.Helper()
	a, err := rig.store.LoadAssets(rig.proj.ID)
	if err != nil {
		t.Fatal(err)
	}
	return a
}

// 场景/道具不走派生:一个叫「临水城·夜」的场景不是谁的造型档。
func TestGenSheetImageScenesNeverDerive(t *testing.T) {
	rig := newSheetRig(t)
	eng := &fakeSheetEngine{}
	j := sheetJob{
		proj: rig.proj, assets: map[string]project.Asset{}, kind: "scenes",
		id: "scene_01", name: "临水城·夜", prompt: "a riverside town at night", ek: "", w: 768, h: 1344, seed: 7,
	}
	if _, err := rig.c.genSheetImage(context.Background(), eng, j); err != nil {
		t.Fatal(err)
	}
	if eng.i2i != 0 || eng.t2i != 1 {
		t.Fatalf("场景不该走角色那套派生,实际 t2i=%d i2i=%d", eng.t2i, eng.i2i)
	}
	if m := rig.findMessage("主卡缺图"); m != "" {
		t.Errorf("场景不该出现「主卡缺图」的降级播报:%s", m)
	}
}

// ---------- 身份校验闭环 ----------

// 校验不过 → 换种子重出一轮;仍不过 → **如实告警**(保留最后一张交给人看)。
func TestDeriveVariantSheetRetriesOnceThenWarns(t *testing.T) {
	rig := newSheetRig(t)
	rig.useVLM(t, varyVLM(t, identityFail, identityFail).URL)
	eng := &fakeSheetEngine{}
	_, assets := rig.masterSheet(t, "char_01v1", "翻篇·收容所")
	j := sheetJob{
		proj: rig.proj, assets: assets, kind: "characters",
		id: "char_01v2", name: "翻篇·进城", prompt: "state", ek: KindSpiritPet, w: 768, h: 1344, seed: 7,
	}
	path, err := rig.c.genSheetImage(context.Background(), eng, j)
	if err != nil {
		t.Fatal(err)
	}
	if path == "" {
		t.Fatal("校验不通过也要保留最后一张 —— 造型档本身是必需的,退回文生图只会更不一致")
	}
	if eng.i2i != 2 {
		t.Fatalf("校验不过应换种子重出一次(共 2 轮,沿用 verifyAttempts 的既有语义),实际 %d 轮", eng.i2i)
	}
	if !strings.Contains(rig.findMessage("第 2 次重出", "翻篇·进城"), "第 2 次重出") {
		t.Errorf("重出要播报(否则用户只看到「卡了一下又出图」),实际事件:%v", rig.messages())
	}
	msg := rig.findMessage("身份校验未通过", "翻篇·进城")
	if msg == "" {
		t.Fatalf("两轮都不通过必须如实告警,实际事件:%v", rig.messages())
	}
	if !strings.Contains(msg, "毛色与印记都变了") {
		t.Errorf("告警要带上校验给的原因,实际:%s", msg)
	}
	if !strings.Contains(msg, "重新生成") {
		t.Errorf("告警要给出下一步(建议重出/核对主卡),实际:%s", msg)
	}
}

// 第一次不过、第二次通过 → 走完两轮,且不再告警(这正是「重试一轮」要救的情形)。
func TestDeriveVariantSheetRetrySucceeds(t *testing.T) {
	rig := newSheetRig(t)
	rig.useVLM(t, varyVLM(t, identityFail, identityPass).URL)
	eng := &fakeSheetEngine{}
	_, assets := rig.masterSheet(t, "char_01v1", "翻篇·收容所")
	j := sheetJob{
		proj: rig.proj, assets: assets, kind: "characters",
		id: "char_01v2", name: "翻篇·进城", prompt: "state", ek: KindSpiritPet, w: 768, h: 1344, seed: 7,
	}
	if _, err := rig.c.genSheetImage(context.Background(), eng, j); err != nil {
		t.Fatal(err)
	}
	if eng.i2i != 2 {
		t.Fatalf("应重出一次,实际 %d 轮", eng.i2i)
	}
	if !strings.Contains(rig.findMessage("身份校验通过", "翻篇·进城"), "身份校验通过") {
		t.Errorf("通过要播报,实际事件:%v", rig.messages())
	}
	if m := rig.findMessage("身份校验未通过"); m != "" {
		t.Errorf("最终通过了就不该再告警,实际:%s", m)
	}
}

// 校验一次就过 → 不重出(重试不能变成"每张都出两遍"的白烧 GPU)。
func TestDeriveVariantSheetNoRetryWhenPass(t *testing.T) {
	rig := newSheetRig(t)
	rig.useVLM(t, varyVLM(t, identityPass).URL)
	eng := &fakeSheetEngine{}
	_, assets := rig.masterSheet(t, "char_01v1", "翻篇·收容所")
	j := sheetJob{
		proj: rig.proj, assets: assets, kind: "characters",
		id: "char_01v2", name: "翻篇·进城", prompt: "state", ek: KindSpiritPet, w: 768, h: 1344, seed: 7,
	}
	if _, err := rig.c.genSheetImage(context.Background(), eng, j); err != nil {
		t.Fatal(err)
	}
	if eng.i2i != 1 {
		t.Fatalf("一次就过不许重出,实际 %d 轮", eng.i2i)
	}
}

// ---------- 参考图来源:本项目优先,其次资产库 ----------

func TestResolveSheetRefFromProjectThenLibrary(t *testing.T) {
	rig := newSheetRig(t)
	// ① 项目里没有同族 → 去资产库找(跨集:主卡是第一集定的妆)
	libImg := filepath.Join(t.TempDir(), "lib-master.png")
	writeTestPNG(t, libImg, 64, 96)
	if _, err := rig.lib.Put("characters", "翻篇·收容所", "同一份形象卡", "PROMPT", libImg, "ep01", "krea2"); err != nil {
		t.Fatal(err)
	}
	j := sheetJob{proj: rig.proj, assets: map[string]project.Asset{}, kind: "characters", id: "char_01v4", name: "翻篇·归零"}
	ref := rig.c.resolveSheetRef(j)
	if ref.Path == "" || ref.Name != "翻篇·收容所" || ref.Via != "资产库" {
		t.Fatalf("本集没有主卡时应去资产库取同族参考,实际 %+v", ref)
	}
	// ② 项目里已有主卡 → **本项目优先**(那是本集这张脸的权威)
	full, assets := rig.masterSheet(t, "char_01v1", "翻篇·收容所")
	j.assets = assets
	ref = rig.c.resolveSheetRef(j)
	if ref.Path != full || ref.Via != "本项目" {
		t.Fatalf("项目里有主卡时必须优先用它,实际 %+v(期望 %s)", ref, full)
	}
	// ③ 主卡自己不是变体 → 不解析参考图(主卡没有更早的档可用)
	ref = rig.c.resolveSheetRef(sheetJob{proj: rig.proj, assets: assets, kind: "characters", id: "char_01v1", name: "翻篇"})
	if ref.Path == "" {
		// 主卡与变体同族:允许解析出"同族其它档",但绝不解析出自己
		t.Log("主卡解析到同族其它档,OK")
	}
	if ref.Path == full {
		t.Error("派生参考不许是它自己")
	}
	// ④ 库里也没有 → 返回空(调用方据此降级并播报)
	ref = rig.c.resolveSheetRef(sheetJob{proj: rig.proj, assets: map[string]project.Asset{}, kind: "characters", id: "char_09v2", name: "老墙·认错"})
	if ref.Path != "" {
		t.Errorf("哪都没有同族卡时必须返回空,实际 %+v", ref)
	}
}
