package project

import "testing"

// 语音核对（ASR）的项目参数：三态开关 + 模型档。
//
// 【为什么开关必须三态】默认是**开**，而 bool 的 false 同时表示"用户关了"和"这个字段还没有"——
// 用 bool 的话，存量项目的 json 里没有这个键，会被静默读成"显式关闭"，于是这条**唯一**能看见
// "音轨里念的是不是台词"的判据在最需要它的老项目上一次都不跑（与 EnableTextDetect 同一课）。
func TestQCParamsASROn(t *testing.T) {
	var q QCParams
	if !q.ASROn() {
		t.Error("没表态（字段缺失）时必须按开处理 —— 否则存量项目会被静默关掉语音核对")
	}
	q.EnableASR = Bool(true)
	if !q.ASROn() {
		t.Error("显式开就是开")
	}
	q.EnableASR = Bool(false)
	if q.ASROn() {
		t.Error("显式关必须真的关得掉")
	}
}

// 模型档：白名单外的一律回落 medium（唯一的判据点，别处不许再判一次）。
func TestQCParamsASRModelOr(t *testing.T) {
	cases := map[string]string{
		"":           "medium", // 没写 = 默认
		"medium":     "medium",
		"large-v3":   "large-v3",
		" small ":    "small",
		"medium2":    "medium", // 拼错/乱填 → 回落
		"whisper-1":  "medium",
		"LARGE-V3":   "medium", // 大小写不匹配就走默认（模型名是工具侧的字面量，不做大小写折叠）
		"large-v3-x": "medium",
	}
	for in, want := range cases {
		q := QCParams{ASRModel: in}
		if got := q.ASRModelOr(); got != want {
			t.Errorf("ASRModelOr(%q) = %q，期望 %q", in, got, want)
		}
	}
}
