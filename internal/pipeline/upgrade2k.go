package pipeline

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/YiIimini/ManJuX/internal/engine"
	"github.com/YiIimini/ManJuX/internal/engine/h3api"
	"github.com/YiIimini/ManJuX/internal/ff"
	"github.com/YiIimini/ManJuX/internal/project"
)

// StartUpgrade2K 异步启动 2K 定稿(纳入项目运行锁,SSE 推进度)。
func (c *Coordinator) StartUpgrade2K(projectID string, shotID int) error {
	c.mu.Lock()
	if _, dup := c.runs[projectID]; dup {
		c.mu.Unlock()
		return fmt.Errorf("项目正在运行中,请先停止")
	}
	if _, ok := c.store.Get(projectID); !ok {
		c.mu.Unlock()
		return fmt.Errorf("项目不存在: %s", projectID)
	}
	ctx, cancel := context.WithCancel(context.Background())
	c.runs[projectID] = &Run{Cancel: cancel, Phase: "upgrade2k", StartedAt: time.Now()}
	c.mu.Unlock()
	go func() {
		defer c.finish(projectID)
		if err := c.Upgrade2K(ctx, projectID, shotID); err != nil && ctx.Err() == nil {
			c.emit(projectID, "pipeline", shotID, "error", 0, "✕ 2K 定稿失败:%v", err)
		}
	}()
	return nil
}

// Upgrade2K 云端 2K 定稿:shotID>0 升级单镜(clips/2k/NN.mp4);
// shotID==0 升级合成成片(output/final_2k.mp4)。
// 官方 regeneration 规格:源须为 H3 768P 产物(短边 768/含音轨/≤50MB)。
func (c *Coordinator) Upgrade2K(ctx context.Context, projectID string, shotID int) error {
	if _, ok := c.store.Get(projectID); !ok {
		return fmt.Errorf("项目不存在: %s", projectID)
	}
	apiEng, err := c.reg.Get("api")
	if err != nil {
		return err
	}
	reg, ok := apiEng.(*h3api.Engine)
	if !ok {
		return fmt.Errorf("云端引擎不支持 2K 再生成")
	}

	var base, out, label string
	var prompt string
	var refs []engine.RefImage
	if shotID > 0 {
		plan, err := c.store.LoadPlan(projectID)
		if err != nil {
			return fmt.Errorf("尚未生成分镜方案")
		}
		manifest, err := c.store.LoadManifest(projectID)
		if err != nil {
			return err
		}
		var shot *project.Shot
		for i := range plan.Shots {
			if plan.Shots[i].ID == shotID {
				shot = &plan.Shots[i]
				break
			}
		}
		if shot == nil {
			return fmt.Errorf("镜头 %d 不存在", shotID)
		}
		m, ok := manifest[strconv.Itoa(shotID)]
		if !ok || m.Status != project.ShotDone || m.File == "" {
			return fmt.Errorf("镜头 %d 尚无已完成产物", shotID)
		}
		base = filepath.Join(c.store.Dir(projectID), m.File)
		out = filepath.Join(c.store.Dir(projectID), "clips", "2k",
			fmt.Sprintf("shot_%03d_2k.mp4", shotID))
		// 2K 重生成必须与 768p 主渲染**同一条提示词**:只带 shot.H3Prompt 的话,
		// 身份锚定与画风硬约束(动漫化/哑光)在 2K 这一道全丢了 —— 实测表现为
		// "768p 是哑光动画脸、升到 2K 变成油光写实脸"。四路渲染统一走 ShotRenderPrompt。
		var projPtr *project.Project
		if p, ok := c.store.Get(projectID); ok {
			projPtr = p
		}
		prompt = ShotRenderPrompt(projPtr, plan, shot)
		label = fmt.Sprintf("镜头 %d", shotID)
		// 参考图沿用渲染期资产(与 render 阶段共用 RefPlan,防止两处漂移)
		//
		// 【2026-09-28 审计 A2】原来这两处是 `assets, _ :=` 与 `refs, roles, _ =` ——
		// **丢掉错误**的后果不是"少一张图"而是**静默换路线**：资产表读不出时 assets 是 nil，
		// RefPlan 装配不出任何参考图，于是这一镜的 2K **不带参考图跑**（云端按文字重画，
		// 人脸与主渲染那版不是同一个），**白花一次云额度，还照报"完成"**。
		// 现在接住并如实播报：宁可让用户看到"这次没带参考图"，也不要交一版不一样的脸。
		assets, aerr := c.store.LoadAssets(projectID)
		if aerr != nil {
			c.emit(projectID, "pipeline", shotID, "warn", 0,
				"⚠ %s 的 2K 定稿读不到资产表(%v)——本次**不带角色参考图**跑,画面里的人脸由模型按文字重画,"+
					"与主渲染那一版不保证是同一个人", label, aerr)
		}
		compact := true
		if proj, ok := c.store.Get(projectID); ok {
			compact = proj.Config.Params.Assets.CompactRefsOn() // 与主渲染保持同一套参考图
		}
		var roles []RefRole
		var missing []string
		refs, roles, missing = RefPlan(c.store.Dir(projectID), *shot, assets, compact)
		if len(missing) > 0 {
			// 【2026-09-28 审计 A2】`missing` 原来是被 `_` 丢掉的 —— 它不是"少一张图"那么轻：
			// 少的那张不会让流程停，而是让这一镜的 2K **按文字重画那个人**，
			// 于是"主渲染一版脸、2K 定稿另一版脸"，而阶段照报"完成"。
			c.emit(projectID, "pipeline", shotID, "warn", 0,
				"⚠ %s 的 2K 缺 %d 张参考图(%s)——缺的那几张不会让流程停,但画面里对应的人/景会**由模型按文字重画**,"+
					"与主渲染那一版不保证一致", label, len(missing), strings.Join(missing, "/"))
		}
		// 与主渲染同一份参考图**权威清单**:单镜 2K 若少了它,升上去的那一版会按
		// 上文猜错的编号理解拼合图(见 refmanifest.go)。
		prompt = EnsureRefManifest(prompt, *shot, roles)
	} else {
		base = filepath.Join(c.store.Dir(projectID), "output", "final.mp4")
		out = filepath.Join(c.store.Dir(projectID), "output", "final_2k.mp4")
		label = "成片"
		// 成片 2K 与其余四条出像素路径**共用同一套介质锁与身份锚定**:
		// 旧实现这里是一句裸字面量提示词(enhance textures, skin detail and
		// micro-structures),refs 也只声明不赋值 —— 一整部哑光动画会被按写实皮肤
		// 先验重画一遍,是"付费把片子改坏"的典型(审计 P0-7)。
		var projPtr *project.Project
		if p, ok := c.store.Get(projectID); ok {
			projPtr = p
		}
		var planPtr *project.Plan
		if pl, err := c.store.LoadPlan(projectID); err == nil {
			planPtr = pl
		}
		prompt = final2KPrompt(projPtr, planPtr)
		// 【审计 A2 同类，源码门咬出】下面 preflight 那段也读一次资产表 —— 同样不能吞错。
		assets, aerr2 := c.store.LoadAssets(projectID)
		if aerr2 != nil {
			c.emit(projectID, "pipeline", shotID, "warn", 0,
				"⚠ 读不出资产表(%v)——本次预校验按无资产处理", aerr2)
		}
		refs = final2KRefs(c.store.Dir(projectID), planPtr, assets)
		if len(refs) > 0 {
			c.emit(projectID, "pipeline", shotID, "info", 3,
				"成片 2K:已带上 %d 个主要角色的定妆照作身份锚定", len(refs))
		}
	}

	if _, err := os.Stat(base); err != nil {
		return fmt.Errorf("%s源视频不存在,请先完成渲染/合成", label)
	}
	c.emit(projectID, "pipeline", shotID, "info", 5, "2K 定稿(%s)预校验…", label)
	if err := h3api.Validate2KBase(c.ff, base, true); err != nil {
		return err
	}
	taskID, err := reg.Regenerate2K(ctx, base, prompt, refs, out, func(pct int, note string) {
		c.emit(projectID, "pipeline", shotID, "info", pct, "%s", note)
	})
	if err != nil {
		c.emit(projectID, "pipeline", shotID, "error", 0, "✕ 2K 定稿失败:%v", err)
		return err
	}
	rel, _ := filepath.Rel(c.store.Dir(projectID), out)
	// 产物校验:下载回来的 2K 文件此前**没有任何检查** —— 云端若返回一个丢音轨、
	// 时长被改、甚至没真放大的片子,平台会把它当交付物留在盘上,还报「✔ 2K 定稿完成」。
	// 源片那侧早有 Validate2KBase 把关(见上),产物这侧不能空着。
	if verr := verify2KArtifact(c.ff, base, out); verr != nil {
		return fmt.Errorf("2K 产物校验未通过:%v —— 文件已保留在 %s,请先播放确认再决定是否使用", verr, filepath.ToSlash(rel))
	}
	if shotID > 0 {
		// 如实说清这个文件是什么:它**不进镜头清单、不参与合成**(合成按 manifest.File
		// 收片),成片里这一镜仍是 768p。旧实现只报「✔ 2K 定稿完成」,用户为单镜 2K
		// 付了云额度,却拿到一个既不在成片里、界面上也无处下载的文件。
		c.emit(projectID, "pipeline", shotID, "success", 100,
			"✔ 2K 定稿完成:%s(任务 %s;这是**独立导出文件**:不进镜头清单、不参与合成,成片这一镜仍为 768p)",
			filepath.ToSlash(rel), taskID)
		return nil
	}
	c.emit(projectID, "pipeline", shotID, "success", 100, "✔ 2K 定稿完成:%s(任务 %s)", rel, taskID)
	return nil
}

// verify2KArtifact 校验云端返回的 2K 产物(探测源片与产物后比对)。
func verify2KArtifact(f *ff.FF, src, out string) error {
	si, err := f.Probe(src)
	if err != nil {
		return fmt.Errorf("源片探测失败:%w", err)
	}
	oi, err := f.Probe(out)
	if err != nil {
		return fmt.Errorf("产物探测失败:%w", err)
	}
	return validate2KArtifact(si, oi)
}

// validate2KArtifact 2K 产物的判据本体(纯函数,便于穷举):
//   - 有画面:宽高非零;
//   - **真的变大了**:像素数严格大于源片(H3 的 768P → 云端再生成 2K);
//   - 音轨还在:源片有音轨时产物也必须有(口型与音轨是这条流水线的核心产物);
//   - 时长没被改:与源片的差不超过公差,否则成片时间轴会与其它镜头对不上。
//
// 公差按时长放宽(0.25 秒与 0.2% 取大):单镜(几秒)按 0.25 秒卡死,成片(几分钟)
// 允许 0.2% 的浮动 —— 云端重编码极可能差一两帧,不该因此把一次成功的定稿判成失败。
func validate2KArtifact(src, out *ff.ProbeInfo) error {
	if src == nil || out == nil {
		return fmt.Errorf("探测结果为空")
	}
	if out.Width <= 0 || out.Height <= 0 {
		return fmt.Errorf("产物没有有效画面(%dx%d)", out.Width, out.Height)
	}
	if out.Width*out.Height <= src.Width*src.Height {
		return fmt.Errorf("产物并未放大(源 %dx%d → 产物 %dx%d)", src.Width, src.Height, out.Width, out.Height)
	}
	if src.HasAudio && !out.HasAudio {
		return fmt.Errorf("产物丢了音轨(源片有音轨)")
	}
	if src.Duration > 0 && out.Duration > 0 {
		tol := 0.25
		if p := src.Duration * 0.002; p > tol {
			tol = p
		}
		if d := out.Duration - src.Duration; d > tol || d < -tol {
			return fmt.Errorf("时长与源片不符(源 %.2fs → 产物 %.2fs,公差 %.2fs)", src.Duration, out.Duration, tol)
		}
	}
	return nil
}
