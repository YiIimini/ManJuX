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
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/YiIimini/ManJuX/internal/sysexec"
)

// VersionItem 版本管理器一行。
type VersionItem struct {
	Kind      string `json:"kind"`      // core / node / model
	Name      string `json:"name"`      // 显示名
	Local     string `json:"local"`     // 本地版本(commit/tag/版本号/文件时间)
	Remote    string `json:"remote"`    // 在线最新(空=无源)
	Repo      string `json:"repo"`      // owner/repo
	Status    string `json:"status"`    // latest / behind / unknown / local / norepo
	Detail    string `json:"detail"`    // 附加信息(模型大小等)
	CheckedAt int64  `json:"checkedAt"` // 在线检查时刻(0=未检查)
}

// VersionReport 版本管理器全量报告。
type VersionReport struct {
	Core  []VersionItem `json:"core"`
	Nodes []VersionItem `json:"nodes"`
	Model []VersionItem `json:"model"`
}

// githubLatest Release/Tag 在线查询(带进程级缓存与单飞)。
var ghLatest = struct {
	sync.Mutex
	cache map[string]cachedVer
}{cache: map[string]cachedVer{}}

type cachedVer struct {
	tag string
	at  time.Time
}

var repoRe = regexp.MustCompile(`github\.com[/:]([^/]+/[^/.]+)(?:\.git)?$`)

// Versions 生成版本报告。online=true 时查 GitHub 对比(慢,点击「检查更新」才走)。
func (e *Engine) Versions(ctx context.Context, online bool) (*VersionReport, error) {
	rep := &VersionReport{}
	root := filepath.Join(e.cfg().RootDir, "ComfyUI")
	if _, err := os.Stat(filepath.Join(root, "main.py")); err != nil {
		return nil, fmt.Errorf("ComfyUI 根目录无效: %s", root)
	}

	// ---- 本体 ----
	core := VersionItem{Kind: "core", Name: "ComfyUI 本体", Local: readCoreVersion(root), Repo: "comfyanonymous/ComfyUI"}
	if c := gitShort(root); c != "" {
		core.Detail = "commit " + c
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
		it := VersionItem{Kind: "node", Name: en.Name()}
		if c := gitShort(dir); c != "" {
			it.Local = gitDescribe(dir)
			if it.Local == "" {
				it.Local = c
			}
			it.Detail = "commit " + c
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
			tag string
			ts  int64
		}
		mu := sync.Mutex{}
		results := map[string]res{}
		query := func(repo string) {
			defer wg.Done()
			tag, ts := fetchLatest(ctx, repo)
			mu.Lock()
			results[repo] = res{tag, ts}
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
		apply := func(repo string) (string, int64) {
			if r, ok := results[repo]; ok {
				return r.tag, r.ts
			}
			return "", 0
		}
		if core.Repo != "" && core.Local != "" {
			core.Remote, core.CheckedAt = apply(core.Repo)
			core.Status = cmpStatus(core.Local, core.Remote)
		} else {
			core.Status = "unknown"
		}
		rep.Core = []VersionItem{core}
		for i := range rep.Nodes {
			if rep.Nodes[i].Repo == "" || rep.Nodes[i].Local == "" {
				rep.Nodes[i].Status = "norepo"
				continue
			}
			rep.Nodes[i].Remote, rep.Nodes[i].CheckedAt = apply(rep.Nodes[i].Repo)
			rep.Nodes[i].Status = cmpStatus(rep.Nodes[i].Local, rep.Nodes[i].Remote)
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

// gitDescribe 可读版本(tag 优先)。
func gitDescribe(dir string) string {
	out, err := sysexec.Hide(exec.Command("git", "-C", dir, "describe", "--tags", "--exact-match", "HEAD")).Output()
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

// fetchLatest 查 GitHub 最新 Release(无 release 退回最新 tag);空串=查询失败。
// 并行查全部仓库;GitHub API 走直连(系统代理常为本机中转,挂掉时会拖死全部请求)。
func fetchLatest(ctx context.Context, repo string) (string, int64) {
	ghLatest.Lock()
	if c, ok := ghLatest.cache[repo]; ok && time.Since(c.at) < 10*time.Minute {
		ghLatest.Unlock()
		return c.tag, c.at.Unix()
	}
	ghLatest.Unlock()

	ctx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	tag := ghGetRelease(ctx, fmt.Sprintf("https://api.github.com/repos/%s/releases/latest", repo))
	if tag == "" {
		// 无正式 release:取最新 tag(第一项即最新推的)
		tag = ghGetFirstTag(ctx, fmt.Sprintf("https://api.github.com/repos/%s/tags?per_page=1", repo))
	}
	now := time.Now()
	ghLatest.Lock()
	ghLatest.cache[repo] = cachedVer{tag: tag, at: now}
	ghLatest.Unlock()
	return tag, now.Unix()
}

// ghClient GitHub API 直连客户端(绕过系统代理;代理失效时直连反而通)。
var ghClient = &http.Client{
	Transport: &http.Transport{Proxy: nil},
}

func ghGetRelease(ctx context.Context, url string) string {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return ""
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	resp, err := ghClient.Do(req)
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
	resp, err := ghClient.Do(req)
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

// cmpStatus 比较本地/在线版本(v 开头前缀归一)。
func cmpStatus(local, remote string) string {
	if remote == "" {
		return "unknown"
	}
	norm := func(s string) string { return strings.TrimPrefix(strings.TrimSpace(s), "v") }
	if norm(local) == norm(remote) || strings.Contains(local, norm(remote)) {
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
