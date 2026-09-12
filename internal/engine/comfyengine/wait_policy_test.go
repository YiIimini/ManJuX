package comfyengine

import (
	"strings"
	"testing"
	"time"
)

// TestDecideWaitQueueDoesNotEatExecBudget 计时口径回归:排队等待不得计入执行预算。
//
// 回归 2026-09-12(用户报障「角色生成失败」「场景也有失败」):旧实现把 deadline 定在
// "提交那一刻",排在前面的长视频任务会把后面所有任务的超时预算一起吃光 —— 定妆照
// 排 10 分钟队就必然被判「渲染超时(单镜上限 10m0s)」,而引擎其实完全正常。
// 实测第03集三张角色定妆照 + 三张场景图接连被杀,镜头因缺参考图降级 t2va。
func TestDecideWaitQueueDoesNotEatExecBudget(t *testing.T) {
	base := time.Date(2026, 9, 12, 9, 0, 0, 0, time.UTC)
	const exec = 10 * time.Minute
	const qcap = 90 * time.Minute

	cases := []struct {
		name    string
		elapsed time.Duration // 提交至今
		execFor time.Duration // 已执行(负值/零表示"还没开始执行")
		want    WaitDecision
		why     string
	}{
		{
			name:    "排队 25 分钟仍未开始执行:不能算超时(旧实现这里已经开始杀任务)",
			elapsed: 25 * time.Minute, want: WaitContinue,
			why: "排队不计入 10 分钟执行上限",
		},
		{
			name:    "排队 25 分钟后开始执行 5 分钟:仍在预算内",
			elapsed: 30 * time.Minute, execFor: 5 * time.Minute, want: WaitContinue,
			why: "执行预算从「开始执行」起算",
		},
		{
			name:    "执行 11 分钟:判定卡死",
			elapsed: 30 * time.Minute, execFor: 11 * time.Minute, want: WaitExecTimeout,
			why: "执行超时防线必须保留,否则卡死会永远等下去",
		},
		{
			name:    "排队 91 分钟:排队上限兜底",
			elapsed: 91 * time.Minute, want: WaitQueueTimeout,
			why: "一直轮不到要如实报「排队超时」,而不是伪装成执行超时",
		},
	}
	for _, c := range cases {
		var execStart time.Time
		if c.execFor > 0 {
			execStart = base.Add(c.elapsed).Add(-c.execFor)
		}
		now := base.Add(c.elapsed)
		if got := DecideWait(now, base, execStart, exec, qcap); got != c.want {
			t.Errorf("%s: DecideWait = %v, want %v(%s)", c.name, got, c.want, c.why)
		}
	}
}

// TestComfyLaunchArgsMemoryFlags 启动参数的内存治理契约。
//
// 背景(2026-09-12 用户报「渲染时 GPU 和内存都快爆了」,实测显存 23529/24463 MiB
// = 96%、ComfyUI 进程占 40.3GB、系统内存仅剩 8GB):H3 主模型 20GB + 视频 VAE 5GB
// 已经超过一张 24GB 卡,再叠加 ComfyUI 的 smart memory(把 TE 15GB / VAE / 主模型
// 尽量留在显存与内存里),每步都在换页。这几个开关是纯内存策略、不动画质,
// 必须有测试钉住——它们被误删/改回字面量,峰值就会悄悄回到 96%。
func TestComfyLaunchArgsMemoryFlags(t *testing.T) {
	args := comfyLaunchArgs("main.py", "8190", 1, false)
	joined := strings.Join(args, " ")
	for _, want := range []string{"--disable-smart-memory", "--cache-none", "--vram-headroom 1", "--port 8190", "--listen 127.0.0.1"} {
		if !strings.Contains(joined, want) {
			t.Errorf("启动参数缺少「%s」:%v", want, args)
		}
	}
	// ck / flash / fast 这几条在 2026-09-10 被用户实证画质劣化后撤下,不得重新出现
	for _, bad := range []string{"--fast", "--use-ck-attention", "--use-flash-attention"} {
		for _, a := range args {
			if a == bad {
				t.Errorf("参数里不得重新出现画质相关开关 %s", bad)
			}
		}
	}
	// Sage 注意力:官方 H3 教程唯一推荐的提速手段(实测 91.6 → 44.8 秒/步)。
	// 只有 env 里装好 sageattention 时才会传(没装就传,ComfyUI 会直接报错)。
	if strings.Contains(joined, "--use-sage-attention") {
		t.Errorf("未启用 Sage 时不应传该参数:%s", joined)
	}
	on := strings.Join(comfyLaunchArgs("main.py", "8190", 1, true), " ")
	if !strings.Contains(on, "--use-sage-attention") {
		t.Errorf("启用 Sage 时必须传 --use-sage-attention:%s", on)
	}
	// 预留值 0 = 交给 ComfyUI 自定,不传该参数
	off := strings.Join(comfyLaunchArgs("main.py", "8190", 0, false), " ")
	if strings.Contains(off, "--vram-headroom") {
		t.Errorf("预留 0 时不应传 --vram-headroom:%s", off)
	}
}

// TestPickSageWheel 轮子匹配规则(纯函数,无需网络)。
//
// 轮子与 torch/CUDA 严格绑定:选错版本要么装不上,要么 import 崩。规则:
// ①只认 win_amd64 + abi3(cp3x 通用);②cu 段要和本机 torch 的 CUDA 主版本一致
// (13.0 → cu130);③轮子要求的 torch 下限不能高于本机(本机 torch 2.11 不能装
// "torch2.12 and higher" 的轮子);④多个候选取版本号最高者。
func TestPickSageWheel(t *testing.T) {
	assets := []string{
		"sageattention-2.2.0+cu128torch2.10.0andhigher.post6-cp310-abi3-win_amd64.whl",
		"sageattention-2.2.0+cu130torch2.9.1.post6-cp310-abi3-win_amd64.whl",
		"sageattention-2.2.0+cu130torch2.10.0andhigher.post6-cp310-abi3-win_amd64.whl",
		"sageattention-2.1.1+cu130torch2.12.0andhigher.post1-cp310-abi3-win_amd64.whl",
		"sageattention-2.2.0+cu130torch2.10.0andhigher.post6-cp310-abi3-linux_x86_64.whl",
	}
	got, err := pickSageWheel(assets, "2.11.0+cu130", "13.0")
	if err != nil {
		t.Fatalf("应能挑到匹配轮子:%v", err)
	}
	if !strings.Contains(got, "cu130torch2.10.0andhigher.post6") {
		t.Errorf("应选 cu130 + torch2.10+ 的最高版本,实际 %s", got)
	}
	// 本机 torch 太老 → 必须报错而不是硬装
	if _, err := pickSageWheel(assets, "2.9.1+cu130", "13.0"); err != nil {
		// torch2.9.1 可装 cu130torch2.9.1 那个
		if !strings.Contains(err.Error(), "没有匹配") {
			t.Errorf("意外错误:%v", err)
		}
	}
	// 完全没有对应 CUDA 版本的轮子 → 明确报错
	if _, err := pickSageWheel(assets, "2.11.0+cu124", "12.4"); err == nil {
		t.Error("无匹配 CUDA 版本时必须报错,不能装错版本")
	}
}

// TestSageCompatDetectsStaleWheel 轮子失配检测(升级后自愈的判据)。
//
// 场景:升级 torch/CUDA 后旧轮子还在,新轮子按新的 cu 段编译 —— 这时必须判定为
// 不匹配(→ 自愈重装、且**不**加 --use-sage-attention),否则轻则 import 失败、
// 重则内核与显存布局不匹配导致出图异常。
func TestSageCompatDetectsStaleWheel(t *testing.T) {
	cases := []struct {
		ver, torch, cuda string
		want             bool
		name             string
	}{
		{"2.2.0+cu130torch2.10.0andhigher.post6", "2.11.0+cu130", "13.0", true, "完全匹配"},
		{"2.2.0+cu128torch2.10.0andhigher.post6", "2.11.0+cu130", "13.0", false, "CUDA 段不符(升级 torch/CUDA 后)"},
		{"2.2.0+cu130torch2.12.0andhigher.post1", "2.11.0+cu130", "13.0", false, "轮子要求更高 torch(装了新版轮子)"},
		{"2.2.0+cu130torch2.9.1.post6", "2.11.0+cu130", "13.0", true, "轮子下限低于本机 → 可用"},
		{"未知", "2.11.0+cu130", "13.0", false, "读不到版本 → 保守判定不匹配"},
		{"2.2.0.post6", "2.11.0+cu130", "13.0", false, "版本串缺 CUDA 构建段 → 保守判定不匹配"},
	}
	for _, c := range cases {
		got, reason := SageCompat(c.ver, c.torch, c.cuda)
		if got != c.want {
			t.Errorf("%s: SageCompat(%q) = %v(%s), want %v", c.name, c.ver, got, reason, c.want)
		}
		if !got && reason == "" {
			t.Errorf("%s: 判定不匹配时必须给出中文原因", c.name)
		}
	}
}
