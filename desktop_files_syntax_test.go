package main

import (
	"context"
	"strings"
	"testing"

	"github.com/alecthomas/chroma/v2"
	"github.com/egoist/mygo/ui"
	"retty/internal/files"
)

func TestFileSyntaxLanguagesPreserveText(t *testing.T) {
	for name, text := range map[string]string{
		"main.go":     "package main\n/* 跨行注释\n still comment */\nfunc main() { println(\"中文\") }",
		"app.tsx":     "// comment\nconst App = () => <div title=\"中文\">Hello</div>;",
		"main.py":     "# comment\ndef greet():\n    return \"中文\"",
		"config.json": "{\"name\": \"中文\", \"enabled\": true, \"count\": 42}",
		"config.yaml": "# comment\nname: \"中文\"\nenabled: true",
		"run.sh":      "#!/bin/sh\n# comment\necho \"中文\"",
		"README.md":   "# 标题\nSome **bold** text and `code`",
		"Dockerfile":  "FROM alpine:3.20\n# comment\nRUN echo hello",
	} {
		t.Run(name, func(t *testing.T) {
			var rows []files.DiffRow
			for i, line := range strings.Split(text, "\n") {
				rows = append(rows, files.DiffRow{Text: line, New: i + 1})
			}
			tokens, language := fileSyntax(context.Background(), name, rows, false)
			if len(tokens) != len(rows) || language == "" {
				t.Fatal("no syntax", language, tokens)
			}
			colored := 0
			plain := ui.Hex("#222222")
			for i, line := range tokens {
				var got strings.Builder
				for _, token := range line {
					got.WriteString(token.text)
					if fileSyntaxColor(token.kind, false, plain) != plain {
						colored++
					}
				}
				if got.String() != rows[i].Text {
					t.Fatal("text changed", got.String(), rows[i].Text)
				}
			}
			if colored == 0 {
				t.Fatal("no token colors")
			}
		})
	}
}

func TestFileSyntaxDiffKeepsOldAndNewLexicalStateSeparate(t *testing.T) {
	rows := files.PatchRows("@@ -1,4 +1,3 @@\n-/* deleted comment\n-deleted body\n-*/\n+const message = \"new string\"\n+// new comment\n shared()\n@@ -30,1 +30,1 @@\n-old()\n+func added() {}\n")
	tokens, language := fileSyntax(context.Background(), "file.go", rows, true)
	if len(tokens) != len(rows) || language != "Go" {
		t.Fatal(tokens, language)
	}
	for i, row := range rows {
		var got strings.Builder
		for _, token := range tokens[i] {
			got.WriteString(token.text)
		}
		if row.Kind == '@' {
			if len(tokens[i]) != 0 {
				t.Fatal("hunk header parsed as code")
			}
			continue
		}
		if got.String() != row.Text {
			t.Fatal("diff text changed", i, got.String(), row.Text)
		}
	}
	if tokens[2][0].kind.Category() != chroma.Comment {
		t.Fatal("multiline old comment lost", tokens[2])
	}
	if tokens[4][0].kind.Category() != chroma.Keyword {
		t.Fatal("old comment leaked into new code", tokens[4])
	}
	if tokens[9][0].kind.Category() != chroma.Keyword {
		t.Fatal("second hunk was not highlighted", tokens[9])
	}
}

func TestFileSyntaxBoundsCancellationAndTheme(t *testing.T) {
	for _, rows := range [][]files.DiffRow{nil, {{Text: strings.Repeat("a", fileSyntaxMaxBytes+1)}}} {
		tokens, _ := fileSyntax(context.Background(), "main.go", rows, false)
		if tokens != nil {
			t.Fatal("highlight bound ignored")
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	tokens, _ := fileSyntax(ctx, "main.go", []files.DiffRow{{Text: "package main"}}, false)
	if tokens != nil {
		t.Fatal("cancel ignored")
	}
	tokens, _ = fileSyntax(context.Background(), "file.unknown-type", []files.DiffRow{{Text: "plain text"}}, false)
	if tokens != nil {
		t.Fatal("unknown file did not remain plain")
	}
	plain := ui.Hex("#222222")
	if fileSyntaxColor(chroma.Keyword, false, plain) == fileSyntaxColor(chroma.Keyword, true, plain) {
		t.Fatal("theme colors identical")
	}
	tokens, _ = fileSyntax(context.Background(), "main.go", []files.DiffRow{{Text: "\tconst message = \"中文\"\r"}}, false)
	if tokens == nil {
		t.Fatal("tabs/CRLF rejected")
	}
	var text strings.Builder
	for _, span := range fileSyntaxSpans(tokens[0], false, plain) {
		text.WriteString(span.Text)
	}
	if text.String() != fileDisplayLine("\tconst message = \"中文\"\r") {
		t.Fatal("selectable spans changed text", text.String())
	}
}
