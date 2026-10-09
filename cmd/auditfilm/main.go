// Command auditfilm 对**已产出/半产出**的成片做一轮确定性体检（整季或单集都行）。
//
// 用法：
//
//	go run ./cmd/auditfilm -project 被开除一年后            # 这本书下的每一集
//	go run ./cmd/auditfilm -ep "第01集" -v                 # 只体检某一集，逐条打印
//	go run ./cmd/auditfilm -project 被开除一年后 -md       # 除了打印，各集再写 output/film_audit.md
//
// 【为什么要有它（2026-10-09 用户：「问题好多啊」）】此前每一层都有自己的门，但**没有一处把
// "这一集到底行不行"合起来说一遍** —— 字幕挂满整镜、同场接缝全叠化、说话人不在画面里还被标成
// 画外音，这些都能从盘上算出来，却要等用户打开成片才看见。这个命令就是那道"开演前的体检"。
//
// 全是**确定性**判定：读盘 + ffprobe，不调模型、不编视频、不改任何文件（除非给 -md）。
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/YiIimini/ManJuX/internal/ff"
	"github.com/YiIimini/ManJuX/internal/filmaudit"
)

func main() {
	var (
		dataDir  = flag.String("data", "data", "数据根目录（默认 data）")
		project  = flag.String("project", "", "只体检项目名包含这个子串的书（空 = 全部）")
		ep       = flag.String("ep", "", "只体检集目录名包含这个子串的集（空 = 该书全部）")
		verbose  = flag.Bool("v", false, "逐条打印（默认只打印 warn/fail 与非零总结）")
		writeMD  = flag.Bool("md", false, "把每集结论写到 <集>/output/film_audit.md")
		ffmpeg   = flag.String("ffmpeg", "", "ffmpeg 路径（默认自动探测）")
		ffprobe  = flag.String("ffprobe", "", "ffprobe 路径（默认自动探测）")
		exitFail = flag.Bool("fail-exit", false, "有 fail 级结论时以退出码 1 结束（写脚本用）")
	)
	flag.Parse()

	probe := probeFn(*ffmpeg, *ffprobe)
	tracks := trackProbeFn(*ffmpeg, *ffprobe)
	eps, err := episodes(filepath.Join(*dataDir, "projects"), *project, *ep)
	if err != nil {
		fmt.Fprintf(os.Stderr, "扫描项目失败：%v\n", err)
		os.Exit(2)
	}
	if len(eps) == 0 {
		fmt.Fprintln(os.Stderr, "没有匹配到的集（检查 -data / -project / -ep）")
		os.Exit(2)
	}
	anyFail := false
	for _, dir := range eps {
		in, inErr := filmaudit.LoadEpisode(dir, probe, tracks)
		if inErr != nil {
			fmt.Printf("⛔ %s：%v\n", filepath.Base(dir), inErr)
			anyFail = true
			continue
		}
		fs := filmaudit.Audit(in)
		fail, warn, info := filmaudit.CountLevels(fs)
		fmt.Printf("\n=== %s  ⛔%d ⚠%d ·%d ===\n", filepath.Base(dir), fail, warn, info)
		for _, f := range fs {
			if !*verbose && f.Level == "info" {
				continue
			}
			fmt.Printf("  %s [%s] %s\n", filmaudit.LevelEmoji(f.Level), f.Where, f.Msg)
		}
		if fail > 0 {
			anyFail = true
		}
		if *writeMD {
			p := filepath.Join(dir, "output", "film_audit.md")
			if werr := os.WriteFile(p, []byte(report(in, fs)), 0o644); werr != nil {
				fmt.Printf("  （写 %s 失败：%v）\n", p, werr)
			} else {
				fmt.Printf("  → 已写 %s\n", p)
			}
		}
	}
	if *exitFail && anyFail {
		os.Exit(1)
	}
}

// probeFn 造一个 ffprobe 实现（探测不到就返回 nil —— 体检会跳过依赖时长的判断并如实说明）。
func probeFn(ffmpegPath, ffprobePath string) filmaudit.ProbeFn {
	f, err := ff.New(ffmpegPath, ffprobePath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "（ffprobe 不可用，跳过时长探测：%v）\n", err)
		return nil
	}
	return func(p string) (float64, error) {
		info, perr := f.Probe(p)
		if perr != nil {
			return 0, perr
		}
		return info.Duration, nil
	}
}

// trackProbeFn 造一个"两条轨各自时长"的探针（2026-10-09 真机 P0：画面被截断、音轨完好时
// format 时长看着是正常的，必须分开量才能发现）。
func trackProbeFn(ffmpegPath, ffprobePath string) filmaudit.TrackProbeFn {
	f, err := ff.New(ffmpegPath, ffprobePath)
	if err != nil {
		return nil
	}
	return func(p string) (float64, float64, bool) {
		info, perr := f.Probe(p)
		if perr != nil || info == nil || info.VideoDuration <= 0 {
			return 0, 0, false
		}
		return info.VideoDuration, info.AudioDuration, true
	}
}

// episodes 找出要体检的集目录（含 plan/plan.json 的目录）。
func episodes(root, project, ep string) ([]string, error) {
	var out []string
	if _, err := os.Stat(root); err != nil {
		return nil, err
	}
	books, err := os.ReadDir(root)
	if err != nil {
		return nil, err
	}
	for _, b := range books {
		if !b.IsDir() {
			continue
		}
		if project != "" && !strings.Contains(b.Name(), project) {
			continue
		}
		subs, serr := os.ReadDir(filepath.Join(root, b.Name()))
		if serr != nil {
			continue
		}
		for _, s := range subs {
			if !s.IsDir() {
				continue
			}
			if ep != "" && !strings.Contains(s.Name(), ep) {
				continue
			}
			dir := filepath.Join(root, b.Name(), s.Name())
			if _, err := os.Stat(filepath.Join(dir, "plan", "plan.json")); err != nil {
				continue
			}
			out = append(out, dir)
		}
	}
	sort.Strings(out)
	return out, nil
}

// report 把结论渲染成一份 Markdown（落在 `output/film_audit.md`）。
func report(in filmaudit.Input, fs []filmaudit.Finding) string {
	fail, warn, info := filmaudit.CountLevels(fs)
	var b strings.Builder
	fmt.Fprintf(&b, "# 成片体检 · %s\n\n", in.ID)
	fmt.Fprintf(&b, "结论：⛔ %d 条必须处理 · ⚠ %d 条该看一眼 · ·%d 条如实告知（确定性判定，读盘即可判）\n\n", fail, warn, info)
	b.WriteString("| 级别 | 位置 | 结论 |\n|---|---|---|\n")
	for _, f := range fs {
		fmt.Fprintf(&b, "| %s | %s | %s |\n", f.Level, f.Where, strings.ReplaceAll(f.Msg, "\n", " "))
	}
	b.WriteString("\n> 口径：`go run ./cmd/auditfilm -project <书名> -md`。只读盘 + ffprobe，" +
		"不调模型、不改文件以外的任何东西。\n")
	return b.String()
}
