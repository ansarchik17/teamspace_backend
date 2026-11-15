package domain_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/west6ide/workspace-backend/internal/domain"
)

func TestNoteTableFormattingRoundTrip(t *testing.T) {
	markup := `<b>Жирный</b> <i>Курсив</i> <s>Старый</s><div><font color="#2458D3">Цвет</font></div>`
	doc := &domain.ContentDoc{Type: "doc", Version: 1, Blocks: []domain.ContentBlock{{ID: "table", Type: "table", Rows: [][]domain.TableCell{{
		{Text: "stale client text", HTML: markup}, {Text: "Legacy <literal> text"},
	}}}}}
	require.NoError(t, doc.Validate())
	first := doc.Blocks[0].Rows[0][0]
	require.Equal(t, markup, first.HTML)
	require.Equal(t, "Жирный Курсив Старый\nЦвет", first.Text)
	data, err := domain.MarshalContentDoc(doc)
	require.NoError(t, err)
	for i := 0; i < 3; i++ {
		restored, err := domain.UnmarshalContentDoc(data)
		require.NoError(t, err)
		require.Equal(t, doc, restored, "repeat saves must not erode formatting")
		require.Contains(t, restored.ExtractPlainText(), first.Text)
		require.NotContains(t, restored.ExtractPlainText(), "<b>")
		data, err = domain.MarshalContentDoc(restored)
		require.NoError(t, err)
	}
}

func TestNoteTableCSSFormattingAndLiteralText(t *testing.T) {
	for _, tc := range []struct{ html, plain, kept string }{
		{`<span style="font-weight: 700; font-style: italic; text-decoration: line-through; color: rgb(36, 88, 211); background-color: #FFFF00">Оформление</span>`, "Оформление", `font-weight: 700; font-style: italic; text-decoration: line-through; color: rgb(36, 88, 211); background-color: #ffff00`},
		{`Line 1<br>Line &amp; 2`, "Line 1\nLine & 2", `Line 1<br>Line &amp; 2`},
		{`&lt;img src=x onerror=alert(1)&gt;`, `<img src=x onerror=alert(1)>`, `&lt;img src=x onerror=alert(1)&gt;`},
	} {
		data, err := json.Marshal(map[string]string{"html": tc.html})
		require.NoError(t, err)
		var cell domain.TableCell
		require.NoError(t, json.Unmarshal(data, &cell))
		require.Equal(t, tc.plain, cell.Text)
		require.Contains(t, cell.HTML, tc.kept)
	}
}

func TestNoteTableRejectsExecutableHTMLAndUnsafeCSS(t *testing.T) {
	markup := `<b onclick="alert(1)">Keep</b><img src="https://tracker.invalid" onerror="alert(1)"><script>alert(2)</script><svg><foreignObject><div>Drop</div></foreignObject></svg><iframe srcdoc="<script>alert(3)</script>"></iframe><span style="color: #2458d3; background-image:url(https://tracker.invalid); position:fixed; width:9999px" onmouseover="alert(4)">Color</span><font color="javascript:alert(5)">Text</font>`
	data, err := json.Marshal(map[string]string{"text": "wrong", "html": markup})
	require.NoError(t, err)
	var cell domain.TableCell
	require.NoError(t, json.Unmarshal(data, &cell))
	require.Equal(t, `<b>Keep</b><span style="color: #2458d3">Color</span><font>Text</font>`, cell.HTML)
	require.Equal(t, "KeepColorText", cell.Text)
	for _, blocked := range []string{"onclick", "onerror", "onmouseover", "script", "svg", "iframe", "url(", "position", "tracker.invalid"} {
		require.NotContains(t, cell.HTML, blocked)
	}
}

func TestNoteTableCellLimitsAndLegacyText(t *testing.T) {
	var legacy domain.TableCell
	require.NoError(t, json.Unmarshal([]byte(`{"text":"Literal <b>text</b>\nNext line"}`), &legacy))
	require.Equal(t, "Literal <b>text</b>\nNext line", legacy.Text)
	require.Empty(t, legacy.HTML)
	for _, field := range []string{"text", "html"} {
		data, err := json.Marshal(map[string]string{field: strings.Repeat("x", 1024*1024+1)})
		require.NoError(t, err)
		var cell domain.TableCell
		require.Error(t, json.Unmarshal(data, &cell))
	}
}
