package files

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

func TestJSONFinalFrameIsUTF8(t *testing.T) {
	sender, receiver := Pipe()
	defer sender.Close()
	ep := newEndpoint(sender, nil, nil, nil)
	const prefix = `{"op":"list","path":"`
	message := msg{Op: "list", Path: strings.Repeat("a", ChunkSize-len(prefix)-1) + "文/end"}
	want, err := json.Marshal(message)
	if err != nil {
		t.Fatal(err)
	}
	if utf8.RuneStart(want[ChunkSize]) {
		t.Fatal("test must put a multibyte rune across the chunk boundary")
	}

	terminal := make(chan []byte, 1)
	var reassembled []byte
	receiver.OnMessage(func(isText bool, data []byte) {
		if isText {
			terminal <- data
		} else {
			reassembled = append(reassembled, data[headerLen:]...)
		}
	})
	if err := ep.send(message); err != nil {
		t.Fatal(err)
	}
	select {
	case final := <-terminal:
		if !utf8.Valid(final) {
			t.Fatalf("final text frame begins inside a UTF-8 rune: %x", final)
		}
		if string(append(reassembled, final...)) != string(want) {
			t.Fatal("fragments do not reconstruct the JSON")
		}
	case <-time.After(time.Second):
		t.Fatal("final text frame never arrived")
	}
}
