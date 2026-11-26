package session

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/tangoui/kkdesk/internal/files"
)

// TestMobileFileHost provides the production LAN signaling and file engine to
// the mobile device smoke test. It never listens during normal Go test runs.
func TestMobileFileHost(t *testing.T) {
	address := os.Getenv("KKDESK_FILE_TEST_LISTEN")
	password := os.Getenv("KKDESK_FILE_TEST_PASSWORD")
	if address == "" || password == "" {
		t.Skip("set KKDESK_FILE_TEST_LISTEN and KKDESK_FILE_TEST_PASSWORD to run the device test host")
	}
	host, err := listenForTest(t, address, password, "")
	if err != nil {
		t.Fatal(err)
	}
	defer host.Close()
	t.Logf("mobile test host listening on %s", host.Addr())
	peer, err := host.Accept(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer peer.Close()
	if peer.Mode() != ModeFiles {
		t.Fatalf("mode = %s, want files", peer.Mode())
	}
	offer := peer.pc.RemoteDescription().SDP
	if strings.Contains(offer, "\nm=audio") || strings.Contains(offer, "\nm=video") {
		t.Error("files-only offer includes media sections")
	}
	server := files.NewServer(peer.FileChannel(), nil)
	server.SetLogf(t.Logf)
	<-server.Done()
}
