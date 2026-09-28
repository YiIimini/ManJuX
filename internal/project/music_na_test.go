package project

import (
	"strings"
	"testing"
)

// `non_diegetic_music` 一律改写成 N/A：**确定性做，不指望模型自觉**。
//
// 平台政策（docs/与官方的有意分歧.md DEV-05）：每个片段都写 N/A，配乐由合片阶段铺一条连续 BGM ——
// 因为官方同时禁止逐镜配乐（STEP 8），逐镜带曲子会让成片在每个接缝处换调。
//
// 这条政策写进提示词后，实测真方案**仍有 19 镜**带着配乐 ⇒ 又一次印证仓库那条纪律：
// 散文规则管不住模型（同 EnsurePropAnchors / 建立镜头串，一律在装配点确定性补上）。
func TestEnsureMusicNARewritesWholeSection(t *testing.T) {
	plan := &Plan{Shots: []Shot{{ID: 1, H3Prompt: strings.Join([]string{
		"subject_definitions:",
		"The narrator is a young woman.",
		"summary:",
		"[reference generation] She walks in.",
		"retention_analysis:",
		"Keep the face.",
		"detailed_description:",
		"She walks toward the camera.",
		"overall_soundscape:",
		"Wind and footsteps.",
		"non_diegetic_music:",
		"A slow guzheng melody rises, then a low string swell,",
		"ending on a single sustained note.",
	}, "\n")}}}

	if n := EnsureMusicNA(plan); n != 1 {
		t.Fatalf("应改写 1 镜，实际 %d", n)
	}
	p := plan.Shots[0].H3Prompt
	if !strings.Contains(p, "non_diegetic_music:\nN/A") {
		t.Errorf("应改写为 N/A，实际:\n%s", p)
	}
	// **整段**换掉：原来那三句配乐描写一句都不该留下（否则残留半句更糟）
	for _, leftover := range []string{"guzheng", "string swell", "sustained note"} {
		if strings.Contains(p, leftover) {
			t.Errorf("配乐描写应整段删除，仍残留 %q", leftover)
		}
	}
	// 其它段必须原样保留（改写只动配乐那一段）
	for _, keep := range []string{"subject_definitions:", "retention_analysis:", "overall_soundscape:", "She walks toward the camera."} {
		if !strings.Contains(p, keep) {
			t.Errorf("不该动到别的段，丢了 %q", keep)
		}
	}
	// 门必须认可改写结果（判据同一套：门说违规、改写说合规，就是两套判据打架）
	if plan.Shots[0].H3Prompt == "" {
		t.Fatal("prompt 不该被清空")
	}
}

// 已经是 N/A 的不动（幂等：装配点可能被调用多次）。
func TestEnsureMusicNAIsIdempotent(t *testing.T) {
	src := "summary:\n[reference generation] x.\nnon_diegetic_music:\nN/A\n"
	plan := &Plan{Shots: []Shot{{ID: 1, H3Prompt: src}}}
	if n := EnsureMusicNA(plan); n != 0 {
		t.Errorf("已是 N/A 不该改写，实际 %d", n)
	}
	if plan.Shots[0].H3Prompt != src {
		t.Errorf("一字不该动，实际:\n%s", plan.Shots[0].H3Prompt)
	}
}

// 行首才认段名：正文里提到这个名字（不是段）不该被当成段头。
func TestEnsureMusicNAIgnoresMentionInProse(t *testing.T) {
	src := "detailed_description:\nThe prompt must leave non_diegetic_music: out of it.\nnon_diegetic_music:\nN/A\n"
	plan := &Plan{Shots: []Shot{{ID: 1, H3Prompt: src}}}
	if n := EnsureMusicNA(plan); n != 0 {
		t.Errorf("正文里提到段名不该被当段头，实际改写 %d 处", n)
	}
}
