package axi

import "testing"

func TestEncodeQuotesWhatWouldChangeMeaning(t *testing.T) {
	doc := NewDoc().
		Set("count", "2 of 9 total").
		Set("id", "1234567890123456789").
		Set("empty", "").
		Set("flag", true).
		Set("total", 7).
		Set("help", []string{"run this", "then, that"})

	want := `count: 2 of 9 total
id: "1234567890123456789"
empty: ""
flag: true
total: 7
help[2]: run this,"then, that"`

	if got := doc.Encode(); got != want {
		t.Fatalf("got:\n%s\nwant:\n%s", got, want)
	}
}

func TestEncodeTabularArrayEscapesCells(t *testing.T) {
	rows := []*Doc{
		NewDoc().Set("id", "301").Set("author", "al,ice").Set("content", "line1\nline2 \"q\""),
		NewDoc().Set("id", "302").Set("author", "bob").Set("content", "ok"),
	}
	want := `messages[2]{id,author,content}:
  "301","al,ice","line1\nline2 \"q\""
  "302",bob,ok`

	if got := NewDoc().Set("messages", rows).Encode(); got != want {
		t.Fatalf("got:\n%s\nwant:\n%s", got, want)
	}
}

func TestEncodeNestedDocumentIndents(t *testing.T) {
	want := `flags:
  "--limit": Messages to fetch
  "--full": Print everything`

	nested := NewDoc().Set("--limit", "Messages to fetch").Set("--full", "Print everything")
	if got := NewDoc().Set("flags", nested).Encode(); got != want {
		t.Fatalf("got:\n%s\nwant:\n%s", got, want)
	}
}
