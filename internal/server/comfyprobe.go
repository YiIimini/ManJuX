// comfyprobe.go ComfyUI 存活探测(带缓存与忙时容错)。
//
// 管线运行时 ComfyUI 满载,/system_stats 响应变慢,原先 2s 超时会误报
// 「离线」→ 底栏灯闪烁。这里改为:6s 超时 + 8s 结果缓存 + 单飞(并发请求
// 共享一次探测);探测失败时保留上一次的在线结论(只在连续失败且上次
// 结论也是在线超过 30s 后才翻转),彻底消除渲染中的状态灯抖动。
package server

import (
	"context"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"
)

type comfyProbe struct {
	mu       sync.Mutex
	lastURL  string
	live     bool // 最近一次有效结论
	at       time.Time
	flipFail int // 连续失败计数(用于从在线翻转为离线)
	inFlight bool
}

var probe = &comfyProbe{}

// probeComfy 探测(8s 缓存内直接复用)。
func (s *Server) probeComfy(baseURL string) bool {
	url := strings.TrimRight(baseURL, "/") + "/system_stats"

	probe.mu.Lock()
	if probe.lastURL == url && time.Since(probe.at) < 8*time.Second {
		live := probe.live
		probe.mu.Unlock()
		return live
	}
	if probe.inFlight { // 单飞:并发者等结论
		for probe.inFlight {
			probe.mu.Unlock()
			time.Sleep(60 * time.Millisecond)
			probe.mu.Lock()
		}
		live := probe.live
		probe.mu.Unlock()
		return live
	}
	probe.inFlight = true
	wasLive := probe.live
	probe.mu.Unlock()

	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	ok := false
	if resp, err := http.DefaultClient.Do(req); err == nil {
		io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<16))
		resp.Body.Close()
		ok = resp.StatusCode == http.StatusOK
	}

	probe.mu.Lock()
	probe.inFlight = false
	probe.lastURL = url
	if ok {
		probe.live, probe.flipFail = true, 0
	} else {
		// 失败一次不翻转在线结论;连续 5 次(约 40s)才判定离线
		probe.flipFail++
		if !wasLive || probe.flipFail >= 5 {
			probe.live = false
		}
	}
	probe.at = time.Now()
	live := probe.live
	probe.mu.Unlock()
	return live
}
