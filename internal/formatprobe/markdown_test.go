// Package formatprobe verifies the chosen gotd CommonMark dialect, not Telethon compatibility.
package formatprobe

import (
	"reflect"
	"strings"
	"testing"

	"github.com/gotd/td/telegram/message/entity"
	"github.com/gotd/td/telegram/message/markdown"
	"github.com/gotd/td/tg"
)

func TestCommonMarkAndUTF16(t *testing.T) {
	for _, tt := range []struct {
		name, input, plain string
		want               tg.MessageEntityClass
	}{
		{"italic", "*text*", "🤖 text", &tg.MessageEntityItalic{Offset: 3, Length: 4}},
		{"double underscore is bold", "__text__", "🤖 text", &tg.MessageEntityBold{Offset: 3, Length: 4}},
		{"emoji before bold", "🙂 **text**", "🤖 🙂 text", &tg.MessageEntityBold{Offset: 6, Length: 4}},
		{"emoji inside bold", "**🙂**", "🤖 🙂", &tg.MessageEntityBold{Offset: 3, Length: 2}},
		{"link", "[text](https://example.org)", "🤖 text", &tg.MessageEntityTextURL{Offset: 3, Length: 4, URL: "https://example.org"}},
		{"inline code", "`code`", "🤖 code", &tg.MessageEntityCode{Offset: 3, Length: 4}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			b := new(entity.Builder)
			b.Plain("🤖 ") // Marker is literal, not parsed as Markdown.
			if err := markdown.Markdown(strings.NewReader(tt.input), b, markdown.Options{}); err != nil {
				t.Fatal(err)
			}
			plain, entities := b.Complete()
			if plain != tt.plain || len(entities) != 1 {
				t.Fatalf("unexpected formatting: %q %#v", plain, entities)
			}
			if !reflect.DeepEqual(entities[0], tt.want) {
				t.Fatalf("wrong entity/UTF-16 offsets: got %#v want %#v", entities[0], tt.want)
			}
		})
	}
}
