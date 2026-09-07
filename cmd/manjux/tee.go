package main

import (
	"io"
)

// teeWriter 同时写文件与标准输出。
type teeWriter struct {
	a, b io.Writer
}

func newTee(a, b io.Writer) io.Writer { return &teeWriter{a, b} }

func (t *teeWriter) Write(p []byte) (int, error) {
	n1, err1 := t.a.Write(p)
	// windowsgui 模式下 os.Stdout 可能无效,忽略其错误
	_, _ = t.b.Write(p)
	if err1 != nil {
		return n1, err1
	}
	return len(p), nil
}
