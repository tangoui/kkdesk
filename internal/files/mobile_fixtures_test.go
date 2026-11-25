package files

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// Regenerate with KKDESK_UPDATE_FILE_FIXTURES=1 go test ./internal/files -run TestMobileFileFixtures.
func TestMobileFileFixtures(t *testing.T) {
	entry := Entry{Name: "文件&<.txt", Path: "C:\\文件&<.txt", Size: 3, ModTime: time.Date(2026, 11, 25, 8, 0, 0, 0, time.UTC), Hidden: true, Link: true}
	const digest = "ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad"
	ok := okMsg(1).OK
	messages := map[string]msg{
		"hello":             {ID: 1, Op: "hello", Version: Version},
		"hello_response":    {ID: 1, OK: ok, Version: Version},
		"home":              {ID: 2, Op: "home"},
		"home_response":     {ID: 2, OK: ok, Home: &Home{Dir: "C:\\Users\\Test", Volumes: []string{"C:\\"}, Sep: "\\", OS: "windows"}},
		"list":              {ID: 3, Op: "list", Path: "C:\\"},
		"list_response":     {ID: 3, OK: ok, Listing: &Listing{Path: "C:\\", Entries: []Entry{entry}, Truncated: true}},
		"stat":              {ID: 4, Op: "stat", Path: entry.Path},
		"stat_response":     {ID: 4, OK: ok, Entry: &entry},
		"mkdir":             {ID: 5, Op: "mkdir", Dir: "C:\\", Rel: "文件", Unique: true, Transfer: 6},
		"download":          {ID: 7, Op: "download", Transfer: 6, Path: entry.Path},
		"download_response": {ID: 7, OK: ok, Transfer: 6, Name: entry.Name, Size: 3, Files: 1, Skipped: []string{"link"}},
		"upload_begin":      {ID: 8, Op: "upload_begin", Transfer: 6, Dir: "C:\\", Rel: entry.Name, Size: 3, Mode: 0o644, MTime: 1795593600000000000},
		"dir":               {Op: "dir", Transfer: 6, Rel: "文件"},
		"file":              {Op: "file", Transfer: 6, Rel: entry.Name, Size: 3},
		"skip":              {Op: "skip", Transfer: 6, Rel: "gone", Size: 2, Error: codeNotFound},
		"file_error":        {Op: "file_error", Transfer: 6, Rel: "broken", Error: codeIO},
		"end":               {Op: "end", Transfer: 6, Rel: entry.Name, SHA256: digest},
		"upload_end":        {ID: 9, Op: "end", Transfer: 6, Rel: entry.Name, Size: 3, SHA256: digest},
		"done":              {Op: "done", Transfer: 6, Size: 3, Files: 1, Failed: []wireFailure{{Rel: "child", Error: codePermission, Detail: "unreadable"}}},
		"cancel":            {ID: 10, Op: "cancel", Transfer: 6},
		"abort":             {Op: "abort", Transfer: 6, Error: codeDenied},
	}
	for i, code := range []string{codeDenied, codeNotFound, codeExists, codeIO, codePermission, codeInvalid, codeCanceled, codeChecksum, codeVersion} {
		messages["error_"+code] = errMsg(uint64(20+i), &RemoteError{Code: code, Detail: "test & detail"})
	}
	dir := "../../mobile/test/fixtures/files"
	update := os.Getenv("KKDESK_UPDATE_FILE_FIXTURES") == "1"
	if update {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	check := func(name string, data []byte) {
		t.Helper()
		path := filepath.Join(dir, name)
		if update {
			if err := os.WriteFile(path, data, 0o644); err != nil {
				t.Fatal(err)
			}
		}
		want, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(want, data) {
			t.Errorf("%s differs from Go encoding", name)
		}
	}
	for name, message := range messages {
		b, err := json.Marshal(message)
		if err != nil {
			t.Fatal(err)
		}
		check(name+".json", b)
	}
	chunk := make([]byte, headerLen+3)
	putHeader(chunk, kindData, 0x0102030405060708, 0x1112131415161718)
	copy(chunk[headerLen:], "abc")
	check("chunk.bin", chunk)
	putHeader(chunk, kindJSON, 0, 0)
	copy(chunk[headerLen:], "{\"x")
	check("json_fragment.bin", chunk)
}
