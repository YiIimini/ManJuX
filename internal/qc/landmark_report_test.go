package qc

import (
	"errors"
	"strings"
	"testing"
)

// TestLandmarkReportThreeStates 空间锚点核查必须**三态留痕**（2026-10-08 深度排查）。
//
// 此前这一步零输出：未配视觉模型 / 场景卡没写地标 / 非建立镜 / 抽帧失败 —— 报告里都看不出来，
// 于是分不清"验过且干净"与"根本没验"（与 SpeechReport / TextDetectReport 同一条纪律）。
func TestLandmarkReportThreeStates(t *testing.T) {
	// ① 验过且干净：Checked=true，摘要里说清看到几个地标，**不许**出现"未核查"。
	ok := LandmarkChecked(&LandmarkVerdict{Seen: []string{"门槛", "立柱"}})
	if !ok.Checked || ok.Bad() {
		t.Fatalf("验过且干净时应当 Checked=true / Bad=false：%+v", ok)
	}
	if s := LandmarkSummary(ok); !strings.Contains(s, "✔") || strings.Contains(s, "未核查") {
		t.Errorf("验过的摘要不对：%q", s)
	}
	// ② 判负：明确缺失 ⇒ Bad，摘要点名缺了什么。
	bad := LandmarkChecked(&LandmarkVerdict{Missing: []string{"立柱"}, Seen: []string{"门槛"}})
	if !bad.Bad() {
		t.Fatalf("有明确缺失时必须判负：%+v", bad)
	}
	if s := LandmarkSummary(bad); !strings.Contains(s, "✗") || !strings.Contains(s, "立柱") {
		t.Errorf("判负摘要该点名缺失的地标：%q", s)
	}
	// ③ 没验：写清为什么，且**不是**"验过且干净"（Checked=false）。
	sk := LandmarkSkipped("未配置视觉模型")
	if sk.Checked || sk.Bad() || sk.Skipped == "" {
		t.Fatalf("没验必须 Checked=false 且带原因：%+v", sk)
	}
	if s := LandmarkSummary(sk); !strings.Contains(s, "未核查") || strings.Contains(s, "✔") {
		t.Errorf("没验的摘要不许像\"通过\"：%q", s)
	}
	// ④ 想验但失败：同样不当作通过（与 SpeechReport.Error 同一口径）。
	fl := LandmarkFailed(errors.New("抽帧失败"))
	if fl.Checked || fl.Error == "" {
		t.Fatalf("失败必须 Checked=false 且带 error：%+v", fl)
	}
	if s := LandmarkSummary(fl); !strings.Contains(s, "不当作通过") {
		t.Errorf("失败的摘要要说清不当作通过：%q", s)
	}
}

// TestReportHasLandmarkField 报告里必须有这个字段（否则前端/下游拿不到三态）。
func TestReportHasLandmarkField(t *testing.T) {
	r := Report{Landmark: LandmarkSkipped("未配置视觉模型")}
	if r.Landmark == nil || r.Landmark.Checked {
		t.Fatalf("Report.Landmark 没接上：%+v", r.Landmark)
	}
}
