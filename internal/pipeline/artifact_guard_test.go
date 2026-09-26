package pipeline

import (
	"testing"

	"github.com/YiIimini/ManJuX/internal/ff"
	"github.com/YiIimini/ManJuX/internal/llm"
)

// 「校验不过就换种子重出」必须真的有一轮重试(审计:VerifyRetries 是死代码)。
//
// 文档一直写着"默认 1",而三个调用方(资产阶段 / 渲染前补齐 / 库「补全多视角」)
// 都只传 Verify、没传 VerifyRetries —— 于是 attempts 恒为 1,换种子重出这条分支
// 永远不执行:VLM 只要判一次不合格,该角色本次就丢弃全部角度帧、退回单张正面照,
// 而日志只说"多视角生成失败",用户不知道这本来是一次重试就能拿到的结果。
func TestVerifyAttemptsDefaultsToOneRetry(t *testing.T) {
	vlm := llm.New("http://127.0.0.1:1", "k", "m", 1)

	// 未表态(0)= 按默认 1 轮重试 → 最多出 2 轮
	if got := verifyAttempts(TurnaroundOptions{Verify: vlm}); got != 1+defaultVerifyRetries {
		t.Errorf("未表态时应为 %d 轮,实际 %d —— 换种子重出成了死代码", 1+defaultVerifyRetries, got)
	}
	if defaultVerifyRetries != 1 {
		t.Errorf("文档口径是「默认 1(=最多出 2 次)」,常量却是 %d", defaultVerifyRetries)
	}
	// 负数(误传)同样兜到默认,不许变成 1(那就等于没有重试)
	if got := verifyAttempts(TurnaroundOptions{Verify: vlm, VerifyRetries: -3}); got != 1+defaultVerifyRetries {
		t.Errorf("非法值应兜到默认,实际 %d", got)
	}
	// 显式指定照做
	if got := verifyAttempts(TurnaroundOptions{Verify: vlm, VerifyRetries: 3}); got != 4 {
		t.Errorf("显式 3 次重试应为 4 轮,实际 %d", got)
	}
	// 没配视觉模型时只跑一轮:判不了"像不像本人",重出没有意义
	if got := verifyAttempts(TurnaroundOptions{VerifyRetries: 5}); got != 1 {
		t.Errorf("无视觉模型时应只跑 1 轮,实际 %d", got)
	}
}

// 云端 2K 产物必须过校验(审计:下载回来的 2K 定稿此前没有任何检查,
// 丢音轨/时长被改/根本没放大都会留在盘上还报「✔ 2K 定稿完成」)。
func TestValidate2KArtifact(t *testing.T) {
	src := &ff.ProbeInfo{Width: 768, Height: 1344, Duration: 5.0, HasAudio: true}
	ok := &ff.ProbeInfo{Width: 1536, Height: 2688, Duration: 5.0, HasAudio: true}
	if err := validate2KArtifact(src, ok); err != nil {
		t.Fatalf("正常的 2K 产物不该被判失败:%v", err)
	}
	cases := []struct {
		name string
		out  *ff.ProbeInfo
	}{
		{"产物没放大(和源片一样大)", &ff.ProbeInfo{Width: 768, Height: 1344, Duration: 5.0, HasAudio: true}},
		{"产物丢了音轨", &ff.ProbeInfo{Width: 1536, Height: 2688, Duration: 5.0}},
		{"产物时长被改", &ff.ProbeInfo{Width: 1536, Height: 2688, Duration: 9.0, HasAudio: true}},
		{"产物没有有效画面", &ff.ProbeInfo{Duration: 5.0, HasAudio: true}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := validate2KArtifact(src, tc.out); err == nil {
				t.Error("这份产物应当判失败 —— 否则坏产物会当交付物留在盘上")
			}
		})
	}
	// 源片没音轨时不该反过来要求产物有音轨(判据只保护"源片有的东西")
	noAudioSrc := &ff.ProbeInfo{Width: 768, Height: 1344, Duration: 5.0}
	if err := validate2KArtifact(noAudioSrc, &ff.ProbeInfo{Width: 1536, Height: 2688, Duration: 5.0}); err != nil {
		t.Errorf("源片无音轨时不该要求产物有音轨:%v", err)
	}
	// 时长公差内(±0.25s)算通过:云端重编码会有零点几秒的偏移
	if err := validate2KArtifact(src, &ff.ProbeInfo{Width: 1536, Height: 2688, Duration: 5.2, HasAudio: true}); err != nil {
		t.Errorf("公差内不该判失败:%v", err)
	}
	// 成片级别(几分钟)按比例放宽:不该因为差一两帧就把一次成功的定稿判成失败
	film := &ff.ProbeInfo{Width: 768, Height: 1344, Duration: 300, HasAudio: true}
	if err := validate2KArtifact(film, &ff.ProbeInfo{Width: 1536, Height: 2688, Duration: 300.5, HasAudio: true}); err != nil {
		t.Errorf("长片的小幅浮动不该判失败:%v", err)
	}
	if err := validate2KArtifact(film, &ff.ProbeInfo{Width: 1536, Height: 2688, Duration: 310, HasAudio: true}); err == nil {
		t.Error("长片差了 10 秒必须判失败(时间轴对不上)")
	}
	if err := validate2KArtifact(nil, ok); err == nil {
		t.Error("探测结果为空时要报错,不能静默通过")
	}
}
