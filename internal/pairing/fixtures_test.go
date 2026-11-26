package pairing

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"math/big"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestPairingFixtures(t *testing.T) {
	hostSeed := bytes.Repeat([]byte{0x11}, 32)
	phoneSeed := bytes.Repeat([]byte{0x22}, 32)
	host := ed25519.NewKeyFromSeed(hostSeed)
	phone := ed25519.NewKeyFromSeed(phoneSeed)
	hostPublic := host.Public().(ed25519.PublicKey)
	phonePublic := phone.Public().(ed25519.PublicKey)
	server := ed25519.NewKeyFromSeed(bytes.Repeat([]byte{0x33}, 32))
	serverPublic := server.Public().(ed25519.PublicKey)
	template := &x509.Certificate{SerialNumber: big.NewInt(1),
		NotBefore: time.Unix(1700000000, 0), NotAfter: time.Unix(4102444800, 0),
		KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
	cert, err := x509.CreateCertificate(rand.Reader, template, template, serverPublic, server)
	if err != nil {
		t.Fatal(err)
	}
	key, err := x509.MarshalPKCS8PrivateKey(server)
	if err != nil {
		t.Fatal(err)
	}
	const code = "T5PM62LR52NUFR43D3VCXWRTXA"
	token := "test-only-token+with-percent-%-and-colon:"
	serverPin := base64.StdEncoding.EncodeToString(serverPublic)
	uri := QRURI(DeviceID(hostPublic), code, Fingerprint(hostPublic), "https://127.0.0.1:8443", token, serverPin,
		[]string{"192.168.1.99:21118", "[fd00::1]:21118"})
	ex := Transcript{Purpose: "pair", ClientNonce: Encode(bytes.Repeat([]byte{0x44}, 32)),
		HostNonce: Encode(bytes.Repeat([]byte{0x55}, 32)), ClientKey: Encode(phonePublic), HostKey: Encode(hostPublic)}
	challenge := Message{Type: "challenge", Nonce: ex.HostNonce, PublicKey: ex.HostKey, Name: "办公电脑", OS: "darwin"}
	ex.Sign(host, &challenge)
	request := Message{Type: "request", Code: ex.CodeProof(code), Name: "测试 iPhone", Model: "iPhone 12 Pro"}
	ex.Sign(phone, &request)
	verification := VerificationCode(code, hostPublic, phonePublic)
	pending := Message{Type: "result", Code: verification, Status: "pending", Deadline: 1800000000000}
	ex.Sign(host, &pending)
	connect := ex
	connect.Purpose = "connect"
	connect.HostNonce = Encode(bytes.Repeat([]byte{0x66}, 32))
	offer := Message{Type: "offer", Mode: "desktop", SDP: "v=0\r\na=ice-pwd:example\r\n"}
	connect.Sign(phone, &offer)
	fixtures := map[string][]byte{
		"uri.txt":        []byte(uri + "\n"),
		"server.pem":     pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: cert}),
		"server-key.pem": pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: key}),
	}
	for name, msg := range map[string]Message{"challenge": challenge, "request": request, "pending": pending, "offer": offer} {
		data, err := json.Marshal(msg)
		if err != nil {
			t.Fatal(err)
		}
		fixtures[name+".json"] = append(data, '\n')
	}
	meta, err := json.MarshalIndent(map[string]string{"phoneSeed": Encode(phoneSeed), "hostSeed": Encode(hostSeed),
		"clientNonce": ex.ClientNonce, "hostNonce": ex.HostNonce, "clientKey": ex.ClientKey, "hostKey": ex.HostKey,
		"verification": verification, "connectNonce": connect.HostNonce, "serverPin": serverPin,
		"challengeBytes": Encode(ex.Bytes(challenge)), "offerBytes": Encode(connect.Bytes(offer))}, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	fixtures["values.json"] = append(meta, '\n')
	dir := filepath.Join("..", "..", "mobile", "test", "fixtures", "pairing")
	for name, data := range fixtures {
		path := filepath.Join(dir, name)
		if os.Getenv("KKDESK_UPDATE_PAIRING_FIXTURES") == "1" {
			if err := os.MkdirAll(dir, 0755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, data, 0644); err != nil {
				t.Fatal(err)
			}
		}
		actual, err := os.ReadFile(path)
		if err != nil || !bytes.Equal(actual, data) {
			t.Errorf("%s differs from Go encoding: %v", name, err)
		}
	}
}
