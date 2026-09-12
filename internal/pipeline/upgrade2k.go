package pipeline

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/YiIimini/ManJuX/internal/engine"
	"github.com/YiIimini/ManJuX/internal/engine/h3api"
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
		prompt = EnsureChineseDialogue(shot.H3Prompt)
		label = fmt.Sprintf("镜头 %d", shotID)
		// 参考图沿用渲染期资产(与 render 阶段共用 AssembleShotRefs,防止两处漂移)
		assets, _ := c.store.LoadAssets(projectID)
		compact := true
		if proj, ok := c.store.Get(projectID); ok {
			compact = proj.Config.Params.Assets.CompactRefsOn() // 与主渲染保持同一套参考图
		}
		refs, _ = AssembleShotRefs(c.store.Dir(projectID), *shot, assets, compact)
	} else {
		base = filepath.Join(c.store.Dir(projectID), "output", "final.mp4")
		out = filepath.Join(c.store.Dir(projectID), "output", "final_2k.mp4")
		label = "成片"
		prompt = "Upscale and refine this video to 2K resolution with In-context Regeneration; keep all subjects, motion, timing, dialogue and audio identical; enhance textures, skin detail and micro-structures."
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
	c.emit(projectID, "pipeline", shotID, "success", 100, "✔ 2K 定稿完成:%s(任务 %s)", rel, taskID)
	return nil
}
