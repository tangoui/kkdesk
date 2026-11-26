package session

import (
	"crypto/hmac"
	"crypto/sha256"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/tangoui/kkdesk/internal/pairing"
	"github.com/tangoui/kkdesk/internal/rendezvous"
)

const (
	remoteHostKeyAttr = "a=x-kkdesk-host-key:"
	remoteHostSigAttr = "a=x-kkdesk-host-sig:"
)

var (
	ErrHostSignature  = errors.New("对方的身份签名无效，已断开（对方可能是旧版本 kkdesk，请两边都升级到最新版）")
	ErrWrongHostCode  = errors.New("对方身份与设备码不符")
	ErrHostKeyChanged = errors.New("这台电脑的身份变了（可能重装了 kkdesk，也可能有人冒充）。如果确认是重装，请在「我的设备」里删除这台设备后重新连接")
)

func signRemoteAnswer(identity *pairing.Store, offer, answer string) string {
	if !strings.HasSuffix(answer, "\r\n") {
		answer += "\r\n"
	}
	return answer + remoteHostKeyAttr + identity.PublicKey() + "\r\n" +
		remoteHostSigAttr + identity.SignHostAnswer(offer, answer) + "\r\n"
}

func stripAuthAttribute(sdp, attr string) (string, string) {
	start := strings.LastIndex(sdp, attr)
	if start < 0 || (start > 0 && sdp[start-1] != '\n') {
		return sdp, ""
	}
	end := strings.Index(sdp[start:], "\r\n")
	if end < 0 {
		return sdp, ""
	}
	end += start
	return sdp[:start] + sdp[end+2:], sdp[start+len(attr) : end]
}

func verifyRemoteAnswer(offer, signedAnswer, code string) (string, error) {
	answer, signature := stripAuthAttribute(signedAnswer, remoteHostSigAttr)
	answer, key := stripAuthAttribute(answer, remoteHostKeyAttr)
	if !pairing.VerifyHostAnswer(key, signature, offer, answer) {
		return "", ErrHostSignature
	}
	public, _ := pairing.PublicKey(key)
	if code = rendezvous.NormalizeCode(code); pairing.DeviceCode(public) != code {
		return "", fmt.Errorf("对方不是设备码 %s 对应的电脑，已断开: %w", code, ErrWrongHostCode)
	}
	return answer, nil
}

type PasswordTranscript struct {
	offer, answer [sha256.Size]byte
}

func NewPasswordTranscript(offer, answer string) PasswordTranscript {
	return PasswordTranscript{sha256.Sum256([]byte(offer)), sha256.Sum256([]byte(answer))}
}

func (t PasswordTranscript) Proof(password string) []byte {
	mac := hmac.New(sha256.New, []byte(password))
	mac.Write([]byte("kkdesk password v2\x00"))
	mac.Write(t.offer[:])
	mac.Write(t.answer[:])
	return mac.Sum(nil)
}

// PasswordGate serializes password attempts across the host's listeners.
type PasswordGate struct {
	mu       sync.Mutex
	failures int
	until    time.Time
}

func (g *PasswordGate) Remaining(now time.Time) time.Duration {
	g.mu.Lock()
	defer g.mu.Unlock()
	return max(0, g.until.Sub(now))
}

// Check returns the matched password, or the time until a paused gate reopens.
// Two zero results represent an incorrect password.
func (g *PasswordGate) Check(now time.Time, transcript PasswordTranscript, proof []byte, temporary, fixed string) (string, time.Duration) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if remaining := g.until.Sub(now); remaining > 0 {
		return "", remaining
	}
	tempOK := hmac.Equal(proof, transcript.Proof(temporary))
	fixedOK := hmac.Equal(proof, transcript.Proof(fixed))
	matched := ""
	if tempOK && temporary != "" {
		matched = temporary
	} else if fixedOK && fixed != "" {
		matched = fixed
	}
	if matched != "" {
		g.failures, g.until = 0, time.Time{}
		return matched, 0
	}

	g.failures = min(g.failures+1, 10)
	if g.failures >= 5 {
		delay := (30 * time.Second) << (g.failures - 5)
		g.until = now.Add(min(delay, 15*time.Minute))
	}
	return "", 0
}
