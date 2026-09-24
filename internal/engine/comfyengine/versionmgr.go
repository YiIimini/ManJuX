// versionmgr.go ComfyUI 版本管理器:本体/插件/模型的本地版本 VS 在线最新。
//
// 本体:comfyui_version.py __version__ + git commit,对比 GitHub Releases。
// 插件:custom_nodes/* 读 .git remote + HEAD,对比对应仓库 Release/Tag。
// 模型:本地文件(大小/时间)——社区模型无统一在线版本源,标注"本地文件"。
// 在线查询结果缓存 10 分钟;网络失败标 unknown,不阻塞页面。
package comfyengine

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/YiIimini/ManJuX/internal/sysexec"
	"golang.org/x/sys/windows/registry"
)

// VersionItem 版本管理器一行。
type VersionItem struct {
	Kind         string `json:"kind"`                   // core / node / model
	Name         string `json:"name"`                   // 显示名
	Local        string `json:"local"`                  // 本地版本(commit/tag/版本号/文件时间)
	LocalCommit  string `json:"localCommit,omitempty"`  // 本地 HEAD 短 commit(VS 主依据)
	Remote       string `json:"remote"`                 // 在线最新(tag / commit)
	RemoteCommit string `json:"remoteCommit,omitempty"` // 远端默认分支 HEAD 短 commit
	Repo         string `json:"repo"`                   // owner/repo
	Status       string `json:"status"`                 // latest / behind / unknown / local / norepo
	Detail       string `json:"detail"`                 // 附加信息(模型大小等)
	Desc         string `json:"desc,omitempty"`         // 中文简要描述(名称下小灰行)
	CheckedAt    int64  `json:"checkedAt"`              // 在线检查时刻(0=未检查)
}

// VersionReport 版本管理器全量报告。
type VersionReport struct {
	Core  []VersionItem `json:"core"`
	Nodes []VersionItem `json:"nodes"`
	Model []VersionItem `json:"model"`
}

// ghLatest 在线版本查询缓存(10 分钟;进程级)。
var ghLatest = struct {
	sync.Mutex
	cache map[string]remoteVer
}{cache: map[string]remoteVer{}}

var repoRe = regexp.MustCompile(`github\.com[/:]([^/]+/[^/.]+)(?:\.git)?$`)

// Versions 生成版本报告。online=true 时查 GitHub 对比(慢,点击「检查更新」才走)。
func (e *Engine) Versions(ctx context.Context, online bool) (*VersionReport, error) {
	rep := &VersionReport{}
	root := filepath.Join(e.cfg().RootDir, "ComfyUI")
	if _, err := os.Stat(filepath.Join(root, "main.py")); err != nil {
		return nil, fmt.Errorf("ComfyUI 根目录无效: %s", root)
	}

	// ---- 本体 ----
	core := VersionItem{Kind: "core", Name: "ComfyUI 本体", Local: readCoreVersion(root), Repo: "comfyanonymous/ComfyUI",
		Desc: "H3 渲染核心主程序"}
	if c := gitShort(root); c != "" {
		core.LocalCommit = c // 版本列显示 @commit,名称列不再重复
	}
	rep.Core = []VersionItem{core}

	// ---- 插件 ----
	cnDir := filepath.Join(root, "custom_nodes")
	entries, _ := os.ReadDir(cnDir)
	for _, en := range entries {
		if !en.IsDir() || strings.HasPrefix(en.Name(), ".") || en.Name() == "__pycache__" {
			continue
		}
		dir := filepath.Join(cnDir, en.Name())
		it := VersionItem{Kind: "node", Name: en.Name(), Desc: nodeDesc(en.Name())}
		if c := gitShort(dir); c != "" {
			it.LocalCommit = c
			it.Local = gitDescribe(dir)
			if it.Local == "" {
				it.Local = c
			}
		}
		if u := gitRemote(dir); u != "" {
			if m := repoRe.FindStringSubmatch(u); m != nil {
				it.Repo = m[1]
			} else {
				it.Detail = strings.TrimSpace(it.Detail + " remote:" + u)
			}
		}
		switch {
		case it.Local == "":
			it.Status = "norepo" // 无 git 元数据的插件(手动拷贝)
		case !online:
			it.Status = "unknown"
		}
		rep.Nodes = append(rep.Nodes, it)
	}

	// ---- 在线对比(并行查,单仓 8s 上限)----
	if online {
		var wg sync.WaitGroup
		type res struct {
			tag, commit string
			ts          int64
		}
		mu := sync.Mutex{}
		results := map[string]res{}
		query := func(repo string) {
			defer wg.Done()
			tag, commit, ts := fetchLatest(ctx, repo)
			mu.Lock()
			results[repo] = res{tag, commit, ts}
			mu.Unlock()
		}
		if core.Repo != "" && core.Local != "" {
			wg.Add(1)
			go query(core.Repo)
		}
		for i := range rep.Nodes {
			if rep.Nodes[i].Repo != "" && rep.Nodes[i].Local != "" {
				wg.Add(1)
				go query(rep.Nodes[i].Repo)
			}
		}
		wg.Wait()
		apply := func(repo string) res {
			if r, ok := results[repo]; ok {
				return r
			}
			return res{}
		}
		if core.Repo != "" && core.Local != "" {
			r := apply(core.Repo)
			core.Remote, core.RemoteCommit, core.CheckedAt = r.tag, r.commit, r.ts
			core.Status = cmpStatus(core.LocalCommit, r.commit, core.Local, r.tag)
		} else {
			core.Status = "unknown"
		}
		rep.Core = []VersionItem{core}
		for i := range rep.Nodes {
			if rep.Nodes[i].Repo == "" || rep.Nodes[i].Local == "" {
				rep.Nodes[i].Status = "norepo"
				continue
			}
			r := apply(rep.Nodes[i].Repo)
			if r.tag != "" && r.commit != "" {
				// tag(版本号)+ commit 双显示
				rep.Nodes[i].Remote = r.tag
			} else if r.commit != "" {
				rep.Nodes[i].Remote = r.commit
			} else {
				rep.Nodes[i].Remote = r.tag
			}
			rep.Nodes[i].RemoteCommit = r.commit
			rep.Nodes[i].CheckedAt = r.ts
			rep.Nodes[i].Status = cmpStatus(rep.Nodes[i].LocalCommit, r.commit, rep.Nodes[i].Local, r.tag)
		}
	} else {
		core.Status = "unknown"
		rep.Core = []VersionItem{core}
	}

	// ---- 模型(本地文件信息,无在线版本源) ----
	if st, err := e.Admin(ctx); err == nil {
		for _, mc := range st.Models {
			it := VersionItem{Kind: "model", Name: mc.Label, Local: mc.Name, Status: "local"}
			if mc.OK {
				if p := findModelFile(root, mc.Name); p != "" {
					if fi, err := os.Stat(p); err == nil {
						it.Detail = fmt.Sprintf("%.1f GB · %s", float64(fi.Size())/1e9, fi.ModTime().Format("2006-01-02"))
					}
				}
			} else {
				it.Status = "missing"
			}
			rep.Model = append(rep.Model, it)
		}
	}
	return rep, nil
}

// readCoreVersion 读 comfyui_version.py 的 __version__(不依赖引擎在线)。
func readCoreVersion(root string) string {
	b, err := os.ReadFile(filepath.Join(root, "comfyui_version.py"))
	if err != nil {
		return ""
	}
	m := regexp.MustCompile(`__version__\s*=\s*"([^"]+)"`).FindSubmatch(b)
	if m == nil {
		return ""
	}
	return string(m[1])
}

// gitShort HEAD 短 commit(无 .git 返回空)。
func gitShort(dir string) string {
	out, err := sysexec.Hide(exec.Command("git", "-C", dir, "rev-parse", "--short", "HEAD")).Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// gitDescribe 可读版本:精确 tag 优先;不在 tag 上时为「最近 tag+距离+commit」(如 v1.30-5-gc9869ea)。
func gitDescribe(dir string) string {
	out, err := sysexec.Hide(exec.Command("git", "-C", dir, "describe", "--tags", "HEAD")).Output()
	if err == nil {
		return strings.TrimSpace(string(out))
	}
	return ""
}

// gitRemote origin 远端 URL。
func gitRemote(dir string) string {
	out, err := sysexec.Hide(exec.Command("git", "-C", dir, "remote", "get-url", "origin")).Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// remoteVer 在线版本:commit(默认分支 HEAD,VS 主依据)+ tag(佐证显示)。
type remoteVer struct {
	tag    string
	commit string
	at     time.Time
}

// fetchLatest 查 GitHub 默认分支最新 commit(版本 VS 主依据)与最新 Release/Tag(佐证)。
// GitHub API 走直连(系统代理常为本机中转,挂掉时会拖死全部请求)。
func fetchLatest(ctx context.Context, repo string) (string, string, int64) {
	ghLatest.Lock()
	if c, ok := ghLatest.cache[repo]; ok && time.Since(c.at) < 10*time.Minute {
		ghLatest.Unlock()
		return c.tag, c.commit, c.at.Unix()
	}
	ghLatest.Unlock()

	ctx, cancel := context.WithTimeout(ctx, 12*time.Second)
	defer cancel()
	// 版本号取最新 tag 为准:ComfyUI 上游并非每版都发 Release(release/latest
	// 停在旧版,tag 已到新版 —— 2026-09-09 实测 release=v0.34.0 / tag=v0.34.6,
	// 用 release 当「在线最新」会把新版本地误判为落后)。
	tag := ghGetFirstTag(ctx, fmt.Sprintf("https://api.github.com/repos/%s/tags?per_page=1", repo))
	if tag == "" {
		tag = ghGetRelease(ctx, fmt.Sprintf("https://api.github.com/repos/%s/releases/latest", repo))
	}
	// commits/HEAD = 默认分支最新提交(与本地 commit 比对的主依据)
	commit := ghGetCommit(ctx, fmt.Sprintf("https://api.github.com/repos/%s/commits/HEAD", repo))
	now := time.Now()
	// 失败(双空)不写缓存:曾把空结果缓存 10 分钟,页面呈现「未检查」且
	// 不再重试,用户看到的版本管理大面积空白(2026-09-09 反馈根因)。
	if tag == "" && commit == "" {
		return "", "", now.Unix()
	}
	ghLatest.Lock()
	ghLatest.cache[repo] = remoteVer{tag: tag, commit: commit, at: now}
	ghLatest.Unlock()
	return tag, commit, now.Unix()
}

func ghGetCommit(ctx context.Context, url string) string {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return ""
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "ManJuX")
	resp, err := ghFetch(ctx, req)
	if err != nil || resp.StatusCode != 200 {
		return ""
	}
	defer resp.Body.Close()
	var m struct {
		SHA string `json:"sha"`
	}
	if json.NewDecoder(resp.Body).Decode(&m) != nil {
		return ""
	}
	if len(m.SHA) > 7 {
		return m.SHA[:7]
	}
	return m.SHA
}

// ghClient GitHub API 直连客户端(绕过系统代理;代理失效时直连反而通)。
var ghClient = &http.Client{
	Transport: &http.Transport{Proxy: nil},
}

// ghProxyClient 系统代理回退客户端(部分网络访问 GitHub 必须走代理;
// 2026-09-09 实测直连超时而系统代理可达)。ProxyFromEnvironment 只认
// 环境变量,Windows 系统代理在注册表 → 显式读取 HKCU Internet Settings。
var ghProxyClient = func() *http.Client {
	u := windowsSystemProxy()
	if u == nil {
		return &http.Client{Transport: &http.Transport{Proxy: http.ProxyFromEnvironment}}
	}
	return &http.Client{Transport: &http.Transport{Proxy: http.ProxyURL(u)}}
}()

// windowsSystemProxy 读注册表系统代理(ProxyEnable=1 时取 ProxyServer;支持 "host:port" 与 "http=...;https=..." 两种格式)。
func windowsSystemProxy() *url.URL {
	k, err := registry.OpenKey(registry.CURRENT_USER,
		`Software\Microsoft\Windows\CurrentVersion\Internet Settings`, registry.QUERY_VALUE)
	if err != nil {
		return nil
	}
	defer k.Close()
	enable, _, err := k.GetIntegerValue("ProxyEnable")
	if err != nil || enable == 0 {
		return nil
	}
	srv, _, err := k.GetStringValue("ProxyServer")
	if err != nil || srv == "" {
		return nil
	}
	// "http=host:port;https=host:port" 格式优先取 https 项
	for _, part := range strings.Split(srv, ";") {
		if strings.HasPrefix(part, "https=") {
			srv = strings.TrimPrefix(part, "https=")
			break
		}
	}
	srv = strings.Split(srv, ";")[0]
	if !strings.Contains(srv, "://") {
		srv = "http://" + srv
	}
	u, err := url.Parse(srv)
	if err != nil || u.Host == "" {
		return nil
	}
	return u
}

// ghFetch 先直连,失败/被限流(403)回退系统代理。双路兜底:
// 直连出口 IP 的 GitHub 匿名配额常被耗尽(2026-09-09 实测 403),
// 而本机代理出口配额独立,走注册表系统代理即可恢复。
func ghFetch(ctx context.Context, req *http.Request) (*http.Response, error) {
	resp, err := ghClient.Do(req)
	if err == nil && resp.StatusCode < 400 {
		return resp, nil
	}
	if resp != nil && resp.Body != nil {
		resp.Body.Close()
	}
	// 重建同参数请求走代理(原请求 body 已消费;GitHub API 查询均为 GET 无 body)
	proxyReq, e := http.NewRequestWithContext(ctx, req.Method, req.URL.String(), nil)
	if e != nil {
		return nil, e
	}
	proxyReq.Header = req.Header.Clone()
	resp2, err2 := ghProxyClient.Do(proxyReq)
	if err2 != nil {
		return nil, err2
	}
	return resp2, nil
}

func ghGetRelease(ctx context.Context, url string) string {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return ""
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "ManJuX")
	resp, err := ghFetch(ctx, req)
	if err != nil || resp.StatusCode != 200 {
		return ""
	}
	defer resp.Body.Close()
	var m map[string]any
	if json.NewDecoder(resp.Body).Decode(&m) != nil {
		return ""
	}
	v, _ := m["tag_name"].(string)
	return v
}

func ghGetFirstTag(ctx context.Context, url string) string {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return ""
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "ManJuX")
	resp, err := ghFetch(ctx, req)
	if err != nil || resp.StatusCode != 200 {
		return ""
	}
	defer resp.Body.Close()
	var arr []map[string]any
	if json.NewDecoder(resp.Body).Decode(&arr) != nil || len(arr) == 0 {
		return ""
	}
	v, _ := arr[0]["name"].(string)
	return v
}

// cmpStatus 版本 VS:commit 匹配为主(本地 HEAD == 远端默认分支 HEAD → 最新),
// tag 字符串比对为辅(commit 查询失败时兜底)。
func cmpStatus(localCommit, remoteCommit, localTag, remoteTag string) string {
	if remoteCommit != "" {
		if localCommit != "" {
			if localCommit == remoteCommit {
				return "latest"
			}
			return "behind" // commit 不同即落后(可能 ahead,但对分发副本按落后提示更新)
		}
		return "unknown"
	}
	if remoteTag == "" {
		return "unknown"
	}
	norm := func(s string) string { return strings.TrimPrefix(strings.TrimSpace(s), "v") }
	if norm(localTag) == norm(remoteTag) || strings.Contains(localTag, norm(remoteTag)) {
		return "latest"
	}
	return "behind"
}

// findModelFile 在常用模型子目录里定位文件。
func findModelFile(root, name string) string {
	if name == "" {
		return ""
	}
	for _, sub := range []string{"models/unet", "models/diffusion_models", "models/checkpoints",
		"models/clip", "models/vae", "models/loras", "models/text_encoders"} {
		p := filepath.Join(root, sub, name)
		if st, err := os.Stat(p); err == nil && !st.IsDir() {
			return p
		}
	}
	return ""
}

// nodeDesc 插件目录名 → 中文简要描述(名称下小灰行);未知插件给通用描述。
func nodeDesc(dir string) string {
	known := map[string]string{
		"ComfyUI-H3-ConditioningCache": "H3 两阶段渲染条件缓存",
		"comfyui-kitchen":              "采样加速内核",
		"ComfyUI-Manager":              "插件与依赖管理",
		"comfyui_controlnet_aux":       "ControlNet 预处理",
		"comfyui-custom-scripts":       "界面增强脚本集",
	}
	if d, ok := known[dir]; ok {
		return d
	}
	return "自定义节点插件"
}
