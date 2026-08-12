package service

import (
	"strings"
	"testing"
)

func TestValidateUsername(t *testing.T) {
	long := strings.Repeat("a", 31)

	tests := []struct {
		name    string
		input   string
		wantErr bool
	}{
		{"中文昵称", "小明", false},
		{"英文昵称", "Alice", false},
		{"含 emoji", "玩家🎮一号", false},
		{"首尾空白被容忍（调用方先 Trim）", "  Alice  ", false},
		{"空串", "", true},
		// 纯空白由调用方 Trim 后变空串拦截；本函数自身不做 Trim
		{"纯空白（调用方已 Trim）", "   ", false},
		{"31 字符超长", long, true},
		{"30 字符恰好", strings.Repeat("字", 30), false},
		{"控制字符", "bad\nname", true},
		{"零宽字符", "bad\u200bname", true},
		{"方向控制字符", "bad\u202ename", true},
		{"BOM", "\ufeffname", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateUsername(tt.input)
			if tt.wantErr && err == nil {
				t.Fatalf("input %q: want error, got nil", tt.input)
			}
			if !tt.wantErr && err != nil {
				t.Fatalf("input %q: unexpected error %v", tt.input, err)
			}
		})
	}
}
