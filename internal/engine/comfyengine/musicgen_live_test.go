package comfyengine

import (
	"encoding/json"

	"github.com/YiIimini/ManJuX/internal/engine"
	"net/http"
	"testing"
	"time"
)

// 配乐图的**实机核对**：把图里的每个节点类名与输入名，拿到本机 ComfyUI 的权威节点表
// （`/object_info`）里对一遍。
//
// 为什么值得固化：ComfyUI 升级会改节点名或输入名，而这类 drift 的症状是"生成失败/产物不对"，
// 排查距离很远。契约测试（musicgen_contract_test.go）钉的是**我们以为的**契约，
// 这条钉的是**本机实际装的**契约 —— 两者都要有。
//
// 服务器不可达时**跳过**（不假装验过）：它只在开发机上能跑，CI/别的机器上没有 ComfyUI。
func TestMusicGraphMatchesInstalledComfyUI(t *testing.T) {
	base := "http://127.0.0.1:8190"
	cli := &http.Client{Timeout: 10 * time.Second}
	resp, err := cli.Get(base + "/object_info")
	if err != nil {
		t.Skipf("本机 ComfyUI 未运行，跳过实机核对:%v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Skipf("ComfyUI 返回 %d，跳过实机核对", resp.StatusCode)
	}
	var oi map[string]struct {
		Input struct {
			Required map[string]json.RawMessage `json:"required"`
			Optional map[string]json.RawMessage `json:"optional"`
		} `json:"input"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&oi); err != nil {
		t.Skipf("节点表解析失败，跳过实机核对:%v", err)
	}
	if len(oi) == 0 {
		t.Skip("节点表为空，跳过实机核对")
	}

	// 逐个节点核对：类名存在 + 我给的输入名都被节点接受 + 节点必填的我都给了
	g := buildMusicGraph(DefaultMusicModels(), musicJobForTest(), 7, "bgm_verify")
	checked := 0
	for id, raw := range g {
		nd, _ := raw.(map[string]any)
		class, _ := nd["class_type"].(string)
		inputs, _ := nd["inputs"].(map[string]any)
		node, ok := oi[class]
		if !ok {
			t.Errorf("节点 %s：本机 ComfyUI 里没有 %q（升级改名了？）", id, class)
			continue
		}
		declared := map[string]bool{}
		for k := range node.Input.Required {
			declared[k] = true
		}
		for k := range node.Input.Optional {
			declared[k] = true
		}
		for k := range inputs {
			if !declared[k] {
				t.Errorf("节点 %s(%s)：我传了输入 %q，节点不认（输入名会被静默忽略或直接校验失败）", id, class, k)
			}
		}
		for k := range node.Input.Required {
			if _, ok := inputs[k]; !ok && !isLinkOrWidgetOptional(class, k) {
				t.Errorf("节点 %s(%s)：节点必填输入 %q 我没给", id, class, k)
			}
		}
		checked++
	}
	if checked == 0 {
		t.Fatal("一个节点都没核对上，说明图是空的")
	}
	t.Logf("实机核对通过：%d 个节点与 %s 上装的 ComfyUI 一致", checked, base)
}

// isLinkOrWidgetOptional 少数节点的"必填"其实由连线提供（本图的输出节点等），
// 这里只对**本图确实没给**的情形放行，避免把"连线即满足"误判成缺输入。
func isLinkOrWidgetOptional(class, input string) bool {
	switch class {
	case "SaveAudioMP3":
		// audio 通常由连线给出；本图给了，只是防御性放行
		return input == "audio"
	}
	return false
}

// musicJobForTest 测试用的配乐任务（写清参数，避免依赖默认值掩盖问题）。
func musicJobForTest() engine.MusicJob {
	return engine.MusicJob{Tags: "calm guzheng, instrumental", Seconds: 60, BPM: 80}
}
