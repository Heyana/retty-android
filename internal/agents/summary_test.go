package agents

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestFinalReplyExcerpt(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"\n# 完成\n\n- **连接问题已修复。** 第二句。", "连接问题已修复。"},
		{"```sh\nrm -rf sample\n```\nAdded the inbox. Tests passed.", "Added the inbox."},
		{"# 标题\n```\ncode only\n```", ""},
		{"结果\u202e\x00已保存。", "结果 已保存。"},
	} {
		if got := SummaryText(tc.in); got != tc.want {
			t.Fatalf("%q => %q, want %q", tc.in, got, tc.want)
		}
	}
	if got := SummaryText(strings.Repeat("中", 300)); utf8.RuneCountInString(got) > 160 {
		t.Fatal("unbounded final reply")
	}
	h, ok := ParseHook("claude", []byte(`{"hook_event_name":"Stop","session_id":"thread","last_assistant_message":"修复已完成。后续说明。"}`))
	if !ok || h.Summary != "修复已完成。" {
		t.Fatal(h, ok)
	}
}
