package llm

import (
	"errors"
	"strings"
)

// ratelimit.go 限流判定：把"接口被限流"与"这次调用本身有问题"分开。
//
// 为什么值得单独一位（2026-09-27 用户报障）：
//
//	⚠ 定妆照「周廷」比例自检失败(未判定,不当作通过):VLM HTTP 429:
//	  {"error":{"code":"1305","message":"该模型当前访问量过大，请您稍后再试"}}
//
// 这条告警本身没错（口径就是"未判定不当作通过"），错的是**行为**：限流是**临时**的、
// 且是**全局**的（同一份 key 的所有调用都受影响），而当时的处理是"每张卡各报一条、
// 然后继续一张张打过去" —— 用户看到的是刷屏，接口那边看到的是继续加压。
// 正确处置是：**认出限流 → 静默一段时间 → 期间不再调用 → 只播报一次**。
//
// 认的形态（各家叫法不同，全都认）：
//   - HTTP 状态码 429（llm 自己的 statusError 带 code）；
//   - 各家错误体里的业务码：MiniMax `"code":"1305"`、OpenAI `rate_limit_exceeded`、
//     通义 `Throttling`、智谱 `1301/1302`；
//   - 文案：访问量过大 / 请求过于频繁 / rate limit / too many requests / quota。
func IsRateLimited(err error) bool {
	if err == nil {
		return false
	}
	var se *statusError
	if errors.As(err, &se) && se.code == 429 {
		return true
	}
	s := strings.ToLower(err.Error())
	for _, needle := range []string{
		"http 429", "status 429", "429:",
		"1305", "1301", "1302", "rate_limit_exceeded", "throttling",
		"rate limit", "too many requests", "quota",
		"访问量过大", "请求过于频繁", "限流", "频率限制",
	} {
		if strings.Contains(s, strings.ToLower(needle)) {
			return true
		}
	}
	return false
}
