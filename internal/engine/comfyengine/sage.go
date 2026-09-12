// sage.go Sage Attention(官方推荐的 H3 提速后端)的自检、匹配、安装与**失配自愈**。
//
// 为什么单独成文并值得这么多代码:这个轮子与 torch/CUDA 版本严格绑定,升级 ComfyUI
// 或 torch 之后旧轮子会失配 —— 表现只是"渲染突然又慢了一倍",界面上完全看不出原因。
// 用户把环境升级交给本应用负责(2026-09-12「都会交给你去升级,每次你处理到位即可」),
// 所以这里必须做到:①能判断"装了但与本机不匹配";②能按本机版本自动装回匹配的轮子;
// ③判断不确定时**绝不**加 --use-sage-attention(宁可不加速,也不能让引擎起不来)。
package comfyengine

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/YiIimini/ManJuX/internal/config"
	"github.com/YiIimini/ManJuX/internal/sysexec"
)

// ---------- Sage Attention 自检与一键安装(2026-09-12) ----------
//
// 为什么需要它:官方 H3 教程推荐的提速手段(Sage Attention,实测 2.04 倍)依赖一个
// **与 torch/CUDA 版本严格绑定**的轮子 —— 一旦升级 ComfyUI 或 torch,旧轮子就失效,
// 表现为"突然又变慢了",而界面上完全看不出原因。这里提供两件事:
//  ① SageStatus:当前是否装了轮子、装的哪个版本、本机 torch/cu 是多少、以及按设置
//     最终会不会启用 —— 让"为什么没加速"一眼可查;
//  ② InstallSage:按本机 torch/cu 从官方推荐的发布源挑一个匹配轮子下载安装
//     (pip --no-deps,只装这一个包,绝不去动 torch),失败也把原因说清楚。

// SageStatus Sage 加速状态。
type SageStatus struct {
	Installed    bool   `json:"installed"`     // standalone-env 里是否有 sageattention 包
	Version      string `json:"version"`       // 已装版本(可读时)
	Enabled      bool   `json:"enabled"`       // 按当前设置重启后是否会启用
	TorchVersion string `json:"torch_version"` // 本机 torch 版本
	CUDAVersion  string `json:"cuda_version"`  // 本机 torch 的 CUDA 版本(如 13.0)
	Detail       string `json:"detail"`        // 中文说明(给用户看)
}

// sagePkgDir standalone-env 里的 sageattention 包目录。
func sagePkgDir(c config.ComfySettings) string {
	return filepath.Join(c.RootDir, "standalone-env", "Lib", "site-packages", "sageattention")
}

// SageInstalled standalone-env 里是否已装 sageattention。
func SageInstalled(c config.ComfySettings) bool {
	st, err := os.Stat(sagePkgDir(c))
	return err == nil && st.IsDir()
}

// torchInfo 读取本机 torch / CUDA 版本(python -c,失败返回空串)。
// 带 10 分钟缓存:管理页每 8 秒轮询一次,每次都起 python 进程会白白拖慢界面。
func (e *Engine) torchInfo() (torchVer, cudaVer string) {
	e.torchMu.Lock()
	if !e.torchAt.IsZero() && time.Since(e.torchAt) < 10*time.Minute {
		tv, cv := e.torchVer, e.torchCudaVer
		e.torchMu.Unlock()
		return tv, cv
	}
	e.torchMu.Unlock()
	tv, cv := e.readTorchInfo()
	e.torchMu.Lock()
	e.torchVer, e.torchCudaVer, e.torchAt = tv, cv, time.Now()
	e.torchMu.Unlock()
	return tv, cv
}

func (e *Engine) readTorchInfo() (torchVer, cudaVer string) {
	c := e.cfg()
	py := filepath.Join(c.RootDir, "standalone-env", "python.exe")
	if _, err := os.Stat(py); err != nil {
		return "", ""
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	out, err := sysexec.Hide(exec.CommandContext(ctx, py, "-c",
		"import torch;print(torch.__version__,'|',torch.version.cuda)")).Output()
	if err != nil {
		return "", ""
	}
	parts := strings.Split(strings.TrimSpace(string(out)), "|")
	if len(parts) != 2 {
		return "", ""
	}
	return strings.TrimSpace(parts[0]), strings.TrimSpace(parts[1])
}

// SageStatus 当前 Sage 状态(含中文结论,供界面直接展示)。
func (e *Engine) SageStatus(ctx context.Context) SageStatus {
	c := e.cfg()
	tv, cv := e.torchInfo()
	st := SageStatus{TorchVersion: tv, CUDAVersion: cv, Installed: SageInstalled(c), Enabled: e.sageEnabled()}
	if !st.Installed {
		st.Detail = fmt.Sprintf("未安装 sageattention 包 → 未启用加速(本机 torch %s / CUDA %s);可点「安装/更新 Sage 加速」按本机版本自动装", tv, cv)
		return st
	}
	st.Version = readSageVersion(filepath.Join(c.RootDir, "standalone-env", "Lib", "site-packages"), tv, cv)
	compat, reason := SageCompat(st.Version, tv, cv)
	switch {
	case !compat:
		st.Detail = fmt.Sprintf("已安装 sageattention %s,但与本机不匹配:%s —— 点「重装/更新轮子」即可按本机版本装回(当前不会启用加速)", st.Version, reason)
	case st.Enabled:
		st.Detail = fmt.Sprintf("已启用加速(sageattention %s,torch %s / CUDA %s → 实测约 2 倍)", st.Version, tv, cv)
	default:
		st.Detail = fmt.Sprintf("已安装 sageattention %s,但当前设置关闭了加速(设置页「Sage 注意力加速」)", st.Version)
	}
	return st
}

// sageVersions 列出所有 sageattention-*.dist-info 里的版本串。
//
// 为什么要列全部:升级场景下会**残留旧版本的 dist-info**(pip 只在版本串相同时才
// 视作同一个包覆盖;从 cu130 轮子换到 cu131 轮子算不同版本,旧目录会留着)。只取
// 第一个会读到陈旧版本 —— 实测就出现过"重装成功但状态仍显示不匹配"。
func sageVersions(sitePackages string) []string {
	ents, err := os.ReadDir(sitePackages)
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range ents {
		n := e.Name()
		if strings.HasPrefix(n, "sageattention-") && strings.HasSuffix(n, ".dist-info") {
			out = append(out, strings.TrimSuffix(strings.TrimPrefix(n, "sageattention-"), ".dist-info"))
		}
	}
	return out
}

// readSageVersion 读当前生效的版本:优先取与本机 torch/CUDA 匹配的那个,都不匹配时
// 取字典序最大的(当作"最新装入",便于界面明确提示失配原因)。
func readSageVersion(sitePackages, torchVer, cudaVer string) string {
	vers := sageVersions(sitePackages)
	if len(vers) == 0 {
		return "未知"
	}
	sort.Strings(vers)
	for _, v := range vers {
		if ok, _ := SageCompat(v, torchVer, cudaVer); ok {
			return v
		}
	}
	return vers[len(vers)-1]
}

// pruneSageDistInfos 清理陈旧的 dist-info(保留 keep 对应的那个 + 与本机匹配的)。
// 残留目录会让"到底装的是哪版"含混不清,状态页就会一直报失配。
func pruneSageDistInfos(sitePackages, keep, torchVer, cudaVer string) {
	for _, v := range sageVersions(sitePackages) {
		if v == keep {
			continue
		}
		if ok, _ := SageCompat(v, torchVer, cudaVer); ok {
			continue
		}
		_ = os.RemoveAll(filepath.Join(sitePackages, "sageattention-"+v+".dist-info"))
		log.Printf("[comfy] 已清理陈旧 Sage 轮子元数据:%s", v)
	}
}

// sageReleaseAPI 官方推荐的 Windows 预编译轮子发布源(ComfyUI 官方 H3 教程指向
// "按 PyTorch/CUDA 安装对应 sageattention 轮子";该源同时提供 sm120/Blackwell 与
// cu130 构建,并含修复"黑图/噪点"越界 bug 的 post6)。
const sageReleaseAPI = "https://api.github.com/repos/woct0rdho/SageAttention/releases/latest"

// pickSageWheel 从 release 资产里挑与本机 torch/cu 匹配的轮子。
// 规则:win_amd64 + abi3(cp3x 通用)+ cu 位数与 torch 的 CUDA 主版本一致 +
// 轮子要求的 torch 下限 ≤ 本机 torch;多个候选取版本最高者。
func pickSageWheel(assets []string, torchVer, cudaVer string) (string, error) {
	cuTag := ""
	if p := strings.Split(cudaVer, "."); len(p) >= 2 { // 13.0 → 130
		cuTag = p[0] + p[1]
	}
	tv := parseTorchVer(torchVer)
	re := regexp.MustCompile(`sageattention-([0-9.]+)\+cu(\d+)torch([0-9.]+)andhigher\.post(\d+)-cp\d+-abi3-win_amd64\.whl`)
	best, bestVer := "", -1.0
	for _, a := range assets {
		m := re.FindStringSubmatch(a)
		if m == nil {
			continue
		}
		if cuTag != "" && m[2] != cuTag {
			continue
		}
		need := parseTorchVer(m[3])
		if tv > 0 && need > tv+0.001 {
			continue // 轮子要求的 torch 比本机新(如 torch2.11 环境装 torch2.12 轮子)
		}
		if v := parseTorchVer(m[1]); v > bestVer {
			best, bestVer = a, v
		}
	}
	if best == "" {
		return "", fmt.Errorf("发布源里没有匹配本机 torch %s / CUDA %s 的轮子", torchVer, cudaVer)
	}
	return best, nil
}

func parseTorchVer(s string) float64 {
	m := regexp.MustCompile(`^(\d+)\.(\d+)`).FindStringSubmatch(strings.TrimSpace(s))
	if m == nil {
		return 0
	}
	var a, b int
	fmt.Sscanf(m[1], "%d", &a)
	fmt.Sscanf(m[2], "%d", &b)
	return float64(a) + float64(b)/100
}

// InstallSage 下载并安装与本机匹配的 Sage 轮子(pip --no-deps,只装这一个包)。
// 返回(已装版本, 中文结果说明)。
func (e *Engine) InstallSage(ctx context.Context, log func(string)) (string, error) {
	c := e.cfg()
	tv, cv := e.torchInfo()
	if tv == "" {
		return "", fmt.Errorf("读不到本机 torch 版本(请确认 ComfyUI 根目录与 standalone-env)")
	}
	if log != nil {
		log(fmt.Sprintf("检测到本机 torch %s / CUDA %s,正在查询匹配的 Sage 轮子…", tv, cv))
	}
	body, err := httpGetJSON(ctx, sageReleaseAPI)
	if err != nil {
		return "", fmt.Errorf("查询发布源失败:%w", err)
	}
	var rel struct {
		TagName string `json:"tag_name"`
		Assets  []struct {
			Name string `json:"name"`
			URL  string `json:"browser_download_url"`
		} `json:"assets"`
	}
	if err := json.Unmarshal(body, &rel); err != nil {
		return "", fmt.Errorf("发布源返回格式异常:%w", err)
	}
	names := make([]string, 0, len(rel.Assets))
	urlByName := map[string]string{}
	for _, a := range rel.Assets {
		names = append(names, a.Name)
		urlByName[a.Name] = a.URL
	}
	wheel, err := pickSageWheel(names, tv, cv)
	if err != nil {
		return "", err
	}
	if log != nil {
		log("命中轮子:" + wheel + "(" + rel.TagName + "),开始下载…")
	}
	dst := filepath.Join(os.TempDir(), wheel)
	if err := downloadFile(ctx, urlByName[wheel], dst); err != nil {
		return "", fmt.Errorf("下载失败:%w", err)
	}
	py := filepath.Join(c.RootDir, "standalone-env", "python.exe")
	cmd := sysexec.Hide(exec.CommandContext(ctx, py, "-m", "pip", "install", "--no-deps", "--force-reinstall", dst))
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("安装失败:%v(%s)", err, tailLines(string(out), 3))
	}
	sitePkg := filepath.Join(c.RootDir, "standalone-env", "Lib", "site-packages")
	ver := readSageVersion(sitePkg, tv, cv)
	pruneSageDistInfos(sitePkg, ver, tv, cv) // 清掉升级残留的旧版本元数据
	if log != nil {
		log("✔ Sage 加速已安装:" + ver + "(重启引擎后生效)")
	}
	return ver, nil
}

// httpGetJSON 带超时与 User-Agent 的 GET(发布源要求 UA)。
func httpGetJSON(ctx context.Context, url string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "ManJuX")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %s", resp.Status)
	}
	return io.ReadAll(io.LimitReader(resp.Body, 4<<20))
}

func downloadFile(ctx context.Context, url, dst string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", "ManJuX")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("HTTP %s", resp.Status)
	}
	f, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer f.Close()
	if _, err := io.Copy(f, resp.Body); err != nil {
		return err
	}
	return f.Close()
}

func tailLines(s string, n int) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, " / ")
}

// ServeSageStatus / InstallSageHTTP 由 server 层调用(见 handlers_comfy.go)。
// 这里只导出可供 HTTP 层复用的两个入口,保持引擎包不依赖 net/http 的响应写法。

// ---------- 失配检测与自愈(2026-09-12:「升级后每次都要到位」) ----------

// SageCompat 已装轮子是否与本机 torch/CUDA 匹配。
//
// 轮子的版本串里带着构建信息,例如
// `2.2.0+cu130torch2.10.0andhigher.post6`:
//   - `cu130` = 用 CUDA 13.0 编译,必须与本机 torch 的 CUDA 主版本一致(13.0 → 130);
//   - `torch2.10.0andhigher` = 要求 torch ≥ 2.10,本机 torch 低于它就不能用。
//
// 任一不符即为失配 —— 这时**不能**加 --use-sage-attention:轻则 import 失败、重则
// 内核与显存布局不匹配导致出图异常,都不如干脆不加速。
func SageCompat(installedVersion, torchVer, cudaVer string) (bool, string) {
	if strings.TrimSpace(installedVersion) == "" || installedVersion == "未知" {
		return false, "读不到已装轮子的版本信息"
	}
	wantCU := ""
	if p := strings.Split(cudaVer, "."); len(p) >= 2 {
		wantCU = p[0] + p[1]
	}
	gotCU := ""
	if m := regexp.MustCompile(`\+cu(\d+)`).FindStringSubmatch(installedVersion); m != nil {
		gotCU = m[1]
	}
	if gotCU == "" {
		return false, "已装轮子的版本串里没有 CUDA 构建段,无法确认匹配"
	}
	if wantCU != "" && gotCU != wantCU {
		return false, fmt.Sprintf("轮子按 CUDA %s 编译,本机 torch 是 CUDA %s(升级后需重装)", gotCU, cudaVer)
	}
	need := 0.0
	if m := regexp.MustCompile(`torch([0-9]+\.[0-9]+)`).FindStringSubmatch(installedVersion); m != nil {
		need = parseTorchVer(m[1])
	}
	if need > 0 && parseTorchVer(torchVer) > 0 && parseTorchVer(torchVer)+0.001 < need {
		return false, fmt.Sprintf("轮子要求 torch ≥ %s,本机是 %s(升级后需重装)", m2s(need), torchVer)
	}
	return true, "与本机 torch/CUDA 匹配"
}

func m2s(v float64) string {
	// 2.10 → "2.10"
	whole := int(v)
	frac := int((v-float64(whole))*100 + 0.5)
	return fmt.Sprintf("%d.%02d", whole, frac)
}

// SageAutoRepair 升级后自愈:已装但与本机不匹配 → 自动装回匹配轮子。
// 返回(true, 说明) 表示"已处理/无需处理",false 表示尝试了但失败(附原因)。
// wantSage 为 false(用户在设置里明确关掉)时不动任何东西。
func (e *Engine) SageAutoRepair(ctx context.Context, wantSage bool, log func(string)) (bool, string) {
	if !wantSage {
		return true, "设置里已关闭 Sage 加速,不做处理"
	}
	c := e.cfg()
	installed := SageInstalled(c)
	tv, cv := e.torchInfo()
	if tv == "" {
		return true, "读不到本机 torch 版本(跳过 Sage 检查)"
	}
	if installed {
		ok, reason := SageCompat(readSageVersion(filepath.Join(c.RootDir, "standalone-env", "Lib", "site-packages"), tv, cv), tv, cv)
		if ok {
			return true, "Sage 轮子与本机匹配,无需重装"
		}
		if log != nil {
			log("⚠ Sage 轮子失配:" + reason + " → 正在按本机版本重装")
		}
		if _, err := e.InstallSage(ctx, log); err != nil {
			return false, fmt.Sprintf("Sage 自愈失败(不影响渲染,只是没有加速):%v", err)
		}
		return true, "Sage 轮子已按本机 torch/CUDA 重装完成"
	}
	return true, "未安装 Sage 加速(如需提速可在 ComfyUI 管理页一键安装)"
}
