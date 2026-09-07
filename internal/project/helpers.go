package project

import (
	"bytes"
	"encoding/json"
)

// jsonMarshal 统一的 JSON 序列化(缩进,尾部换行)。
func jsonMarshal(v any) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// jsonUnmarshal 统一的 JSON 反序列化。
func jsonUnmarshal(s string, v any) error {
	return json.Unmarshal([]byte(s), v)
}
