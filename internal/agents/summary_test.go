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

func TestStructuredSummaryExcerpt(t *testing.T) {
	for _, tc := range []struct{ name, in, want string }{
		{"JSON", `{"summary":"relay 已收敛为统一转发流程。测试通过。"}`, "relay 已收敛为统一转发流程。"},
		{"pretty JSON", "{\n  \"summary\": \"连接已修复。\",\n  \"checks\": [\"passed\"]\n}", "连接已修复。"},
		{"other fields", `{"checks":["passed"],"summary":"已完成。"}`, "已完成。"},
		{"fenced JSON", "```json\n{\"summary\":\"已完成。\"}\n```", "已完成。"},
		{"escaped text", `{"summary":"已保存到 C:\\work。\n后续说明。"}`, `已保存到 C:\work。`},
		{"old sentence clip", `{"summary":"relay 已收敛为统一转发流程。`, "relay 已收敛为统一转发流程。"},
		{"old length clip", `{"summary":"已完成部分变更`, "已完成部分变更"},
		{"invalid escape", `{"summary":"结果\q`, ""},
		{"wrong type", `{"summary":{"result":"已完成"}}`, ""},
		{"empty", `{"summary":" "}`, ""},
		{"null", `{"summary":null}`, ""},
		{"title remains available", `{"title":"内部命名"}`, `{"title":"内部命名"}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := SummaryText(tc.in); got != tc.want {
				t.Fatalf("got %q, want %q", got, tc.want)
			}
		})
	}
	if got := SummaryText(`{"summary":"` + strings.Repeat("中", 300) + `"}`); utf8.RuneCountInString(got) > 160 || strings.Contains(got, "summary") {
		t.Fatal("structured summary was not clipped after decoding", got)
	}
	h, ok := ParseHook("claude", []byte(`{"hook_event_name":"Stop","session_id":"thread","last_assistant_message":"{\"summary\":\"修复已完成。后续说明。\"}"}`))
	if !ok || h.Summary != "修复已完成。" {
		t.Fatal(h, ok)
	}
}
