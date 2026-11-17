package telegram

import (
	"github.com/gotd/td/tg"
	"testing"
)

func TestPreLanguageAndEntityOffsets(t *testing.T) {
	for _, tt := range []struct{ mode, source string }{
		{"md", "```go\ncode\n```"},
		{"html", "<pre><code class=\"language-go\">code</code></pre>"},
	} {
		t.Run(tt.mode, func(t *testing.T) {
			o := operationFor(nil)
			text, entities, err := o.format(Request{Text: tt.source, ParseMode: tt.mode}, false)
			if err != nil || text != "🤖 code" || len(entities) != 1 {
				t.Fatalf("pre formatting failed: text=%q entities=%#v err=%v", text, entities, err)
			}
			pre, ok := entities[0].(*tg.MessageEntityPre)
			if !ok || pre.Offset != 3 || pre.Length != 4 || pre.Language != "go" {
				t.Fatalf("wrong pre entity: %#v", entities[0])
			}
		})
	}
}
