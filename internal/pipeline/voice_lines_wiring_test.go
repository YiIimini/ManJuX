package pipeline

import (
	"strings"
	"testing"
)

// voice_lines_wiring_test.go 同镜多说话人的**逐句**音色判定接线门（2026-10-11）。
//
// 【要挡什么】判据（qc.PerLineVoiceFindings / MeasureVoicePitchMixed）本身有门，但"接没接起来"是另一件事：
// 这一轮之前，同镜多说话人的镜**整镜基频分不开 ⇒ 一声都不判**（真机镜 9/13 就是这么被跳过的）。
// 接线的三个要点：① 只在 QC 阶段接（那时刚跑过 ASR，有逐词时间轴）；② 要带上这一镜的 ASR 侧车路径；
// ③ 只有逐句那条路**真的判过**（Checked）才用它，否则保留整镜那条"不判"的说明。

func TestVoicePitchPerLineIsWiredOnlyWhereThereIsAWordStream(t *testing.T) {
	src := readSrc(t, "voice_check.go")
	const call = "qc.MeasureVoicePitchMixed("
	at := strings.Index(src, call)
	if at < 0 {
		t.Fatal("没有任何地方调 qc.MeasureVoicePitchMixed —— 同镜多说话人的镜又变成\"一声都不判\"")
	}
	if strings.Count(src, call) != 1 {
		t.Errorf("逐句那条路该只在一个地方接（QC），实际出现 %d 次 —— 渲染期没有词流，接多了就是白解一次码",
			strings.Count(src, call))
	}
	// ① 必须落在 voicePitchForQC 里（QC 阶段：语音核对刚跑过，逐词时间轴在盘上）。
	body := funcBody(t, "voice_check.go", "func voicePitchForQC(")
	if !strings.Contains(body, call) {
		t.Error("逐句那条路没接在 voicePitchForQC 里 —— QC 是唯一有词流的时刻")
	}
	// ② 必须把这一镜的 ASR 侧车路径传进去（不然逐句归属没有时间轴）。
	if !strings.Contains(body, "filmaudit.ASRSidecarPath(clip)") {
		t.Error("没把这一镜的 ASR 侧车路径传进去 —— 逐句归属要它才成立")
	}
	// ③ 先问该不该退（VoiceLinesNeedPerLine），且**只有真判过**才采用。
	if !strings.Contains(body, "qc.VoiceLinesNeedPerLine(lines)") {
		t.Error("没先问\"这一镜是不是整镜分不开\"—— 那会给单性别镜白解一次码")
	}
	if !strings.Contains(body, "per != nil && per.Checked") {
		t.Error("逐句那条路的结论**只有 Checked 才能采用** —— 否则会把\"没判\"摇身变成结论")
	}
	// 渲染期那条路不许接：那时片段刚渲完、ASR 还没转写，没有词流可用。
	if rt := funcBody(t, "voice_check.go", "func (c *Coordinator) qualityRetryTargets("); strings.Contains(rt, call) {
		t.Error("渲染期（qualityRetryTargets）不该接逐句那条路 —— 那时还没有 ASR 词流")
	}
}

// 与之配套：整镜那条路必须**仍然**在"整镜分不开"时如实说不判（别为了新的逐句路把老的说明删了）。
func TestWholeShotPitchStillSaysItCannotJudge(t *testing.T) {
	src := readSrc(t, "../../internal/qc/pitch.go")
	for _, want := range []string{
		"本镜同时有男声和女声说话人，整镜基频分不开，不判音色",
		"本镜既有写性别的角色、也有中性声线的说话人，整镜基频分不开，不判音色",
	} {
		if !strings.Contains(src, want) {
			t.Errorf("整镜判不了时该如实说清（缺这句）：%s", want)
		}
	}
}
