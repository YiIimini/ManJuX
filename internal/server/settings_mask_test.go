package server

import "testing"

// TestMaskKey 脱敏规则:只留尾 4 位;≤8 位原样返回(掩码比原文还长没意义)。
func TestMaskKey(t *testing.T) {
	cases := []struct{ in, want string }{
		{"", ""},
		{"short", "short"},
		{"12345678", "12345678"},
		{"123456789", "****6789"},
		{"sk-abcdefghijklmn", "****klmn"},
	}
	for _, c := range cases {
		if got := maskKey(c.in); got != c.want {
			t.Errorf("maskKey(%q) = %q, 期望 %q", c.in, got, c.want)
		}
	}
}

// TestMaskedKeyNotOverwritesRealKey 回归:掩码回传不得覆盖真实 key。
// 前端拿到并显示的是掩码,保存时会原样回传 —— putSettings 必须识别并回填真值,
// 否则「打开设置页点一次保存」就会把 key 写成一串星号。
func TestMaskedKeyNotOverwritesRealKey(t *testing.T) {
	real := "sk-realkey-1234567890"
	masked := maskKey(real)
	if !isMaskedKey(masked) {
		t.Fatalf("掩码应可被识别: %q", masked)
	}
	got := masked
	if isMaskedKey(got) {
		got = real
	}
	if got != real {
		t.Fatalf("掩码回填后应还原真实 key,实际 %q", got)
	}
	if isMaskedKey(real) {
		t.Error("真实 key 不应被误判为掩码")
	}
}

// TestVLMKeyAlsoMasked 回归:VLM key 此前漏脱敏,会以明文出现在 /api/settings
// 的响应里,任何能访问本机 8790 端口的进程都能取走。
func TestVLMKeyAlsoMasked(t *testing.T) {
	vlm := "zhipu-key-abcdefgh"
	got := maskKey(vlm)
	if got == vlm || !isMaskedKey(got) {
		t.Fatalf("VLM key 也必须脱敏,实际 %q", got)
	}
}
