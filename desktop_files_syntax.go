package main

import (
	"context"
	"strings"
	"time"

	"github.com/alecthomas/chroma/v2"
	"github.com/alecthomas/chroma/v2/lexers"
	"github.com/egoist/mygo/ui"
	"retty/internal/files"
)

const fileSyntaxMaxBytes = 1 << 20
const fileSyntaxMaxTokens = 200000

type fileSyntaxToken struct {
	text string
	kind chroma.TokenType
}

// Tokenize once in the preview worker. Rendering applies the current theme to
// these cached tokens, so a theme change needs neither another read nor lexing.
func fileSyntax(ctx context.Context, name string, rows []files.DiffRow, diff bool) (out [][]fileSyntaxToken, language string) {
	// Third-party lexers must never turn a readable file into a failed preview.
	defer func() {
		if recover() != nil {
			out, language = nil, ""
		}
	}()
	bytes := 0
	for _, r := range rows {
		bytes += len(r.Text) + 1
		if bytes > fileSyntaxMaxBytes {
			return nil, ""
		}
	}
	if len(rows) == 0 || ctx.Err() != nil {
		return nil, ""
	}
	lexer := lexers.Match(name)
	if lexer == nil {
		return nil, ""
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	out = make([][]fileSyntaxToken, len(rows))
	budget := fileSyntaxMaxTokens
	colorRows := func(indices []int) bool {
		if len(indices) == 0 {
			return true
		}
		lines := make([]string, len(indices))
		for i, index := range indices {
			lines[i] = fileDisplayLine(rows[index].Text)
		}
		tokens := fileSyntaxLines(ctx, lexer, lines, &budget)
		if tokens == nil {
			return false
		}
		for i, index := range indices {
			out[index] = tokens[i]
		}
		return true
	}
	if !diff {
		indices := make([]int, len(rows))
		for i := range indices {
			indices[i] = i
		}
		if !colorRows(indices) {
			return nil, ""
		}
	} else {
		// Reconstruct each side of each hunk independently. Deleted strings or
		// comments must not change the lexical state of newly added code.
		var old, new []int
		flush := func() bool { ok := colorRows(old) && colorRows(new); old, new = nil, nil; return ok }
		for i, row := range rows {
			switch row.Kind {
			case '@':
				if !flush() {
					return nil, ""
				}
			case '-':
				old = append(old, i)
			case '+':
				new = append(new, i)
			case ' ':
				old = append(old, i)
				new = append(new, i)
			}
		}
		if !flush() {
			return nil, ""
		}
	}
	return out, lexer.Config().Name
}

func fileSyntaxLines(ctx context.Context, lexer chroma.Lexer, lines []string, budget *int) [][]fileSyntaxToken {
	it, err := lexer.Tokenise(&chroma.TokeniseOptions{State: "root"}, strings.Join(lines, "\n")+"\n")
	if err != nil {
		return nil
	}
	out := make([][]fileSyntaxToken, len(lines))
	row := 0
	for {
		if ctx.Err() != nil || *budget <= 0 {
			return nil
		}
		token := it()
		if token == chroma.EOF {
			break
		}
		*budget -= 1
		value := token.Value
		for value != "" && row < len(lines) {
			part, rest, found := strings.Cut(value, "\n")
			if part != "" {
				out[row] = append(out[row], fileSyntaxToken{text: part, kind: token.Type})
			}
			if !found {
				break
			}
			row++
			value = rest
		}
	}
	// Some lexers normalize input; never alter the text the user can select.
	for i, line := range lines {
		var text strings.Builder
		for _, token := range out[i] {
			text.WriteString(token.text)
		}
		if text.String() != line {
			return nil
		}
	}
	return out
}

func fileSyntaxColor(kind chroma.TokenType, dark bool, plain ui.Color) ui.Color {
	pick := func(light, night string) ui.Color {
		if dark {
			return ui.Hex(night)
		}
		return ui.Hex(light)
	}
	switch kind.Category() {
	case chroma.Comment:
		return pick("#687482", "#8d99a8")
	case chroma.Keyword:
		return pick("#8250df", "#c792ea")
	}
	switch kind.SubCategory() {
	case chroma.LiteralString:
		return pick("#116329", "#a3d98e")
	case chroma.LiteralNumber:
		return pick("#0550ae", "#e7b77b")
	}
	switch kind {
	case chroma.NameFunction, chroma.NameFunctionMagic, chroma.NameBuiltin, chroma.NameDecorator:
		return pick("#0969da", "#82aaff")
	case chroma.NameClass, chroma.NameTag, chroma.NameAttribute:
		return pick("#953800", "#f3b779")
	case chroma.Operator:
		return pick("#8250df", "#c792ea")
	case chroma.GenericHeading, chroma.GenericSubheading:
		return pick("#0969da", "#82aaff")
	}
	return plain
}

func fileSyntaxSpans(tokens []fileSyntaxToken, dark bool, plain ui.Color) []ui.Span {
	var spans []ui.Span
	var text strings.Builder
	color := plain
	flush := func() {
		if text.Len() > 0 {
			spans = append(spans, ui.Span{Text: text.String(), Color: color})
			text = strings.Builder{}
		}
	}
	for _, token := range tokens {
		next := fileSyntaxColor(token.kind, dark, plain)
		if next != color {
			flush()
			color = next
		}
		text.WriteString(token.text)
	}
	flush()
	return spans
}
