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

// PasswordGate rate-limits password attempts per source: a LAN address, or
// all cross-network requests together. A source that keeps failing pauses
// only itself, so a stranger cannot lock everyone else out; failures are
// forgotten after a quiet spell, and Reset (a new password) lifts every
// pause.
type PasswordGate struct {
	mu      sync.Mutex
	sources map[string]*gateSource
}

type gateSource struct {
	failures int
	until    time.Time // paused until
	last     time.Time // latest failure
}

const (
	// gateForget is how long a source's failures are remembered after its
	// latest one (once any pause has ended).
	gateForget = 15 * time.Minute
	// maxGateSources bounds the sources remembered at once.
	maxGateSources = 256
)

// Remaining returns the longest pause still running for any source.
func (g *PasswordGate) Remaining(now time.Time) time.Duration {
	g.mu.Lock()
	defer g.mu.Unlock()
	var longest time.Duration
	for _, s := range g.sources {
		longest = max(longest, s.until.Sub(now))
	}
	return longest
}

// Reset forgets every failure and lifts every pause (the password changed,
// so earlier guesses no longer matter).
func (g *PasswordGate) Reset() {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.sources = nil
}

func (g *PasswordGate) sourceCount() int {
	g.mu.Lock()
	defer g.mu.Unlock()
	return len(g.sources)
}

func (s *gateSource) stale(now time.Time) bool {
	return !now.Before(s.until) && now.Sub(s.last) > gateForget
}

// track returns the record for source, making room if needed. g.mu is held.
func (g *PasswordGate) track(now time.Time, source string) *gateSource {
	if s := g.sources[source]; s != nil {
		return s
	}
	if g.sources == nil {
		g.sources = map[string]*gateSource{}
	}
	if len(g.sources) >= maxGateSources {
		for k, s := range g.sources {
			if s.stale(now) {
				delete(g.sources, k)
			}
		}
	}
	for len(g.sources) >= maxGateSources { // still full: drop the oldest
		oldest := ""
		for k, s := range g.sources {
			if oldest == "" || s.last.Before(g.sources[oldest].last) {
				oldest = k
			}
		}
		delete(g.sources, oldest)
	}
	s := &gateSource{}
	g.sources[source] = s
	return s
}

// Check returns the matched password, or the time until source's pause ends.
// Two zero results represent an incorrect password.
func (g *PasswordGate) Check(now time.Time, source string, transcript PasswordTranscript, proof []byte, temporary, fixed string) (string, time.Duration) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if s := g.sources[source]; s != nil {
		if remaining := s.until.Sub(now); remaining > 0 {
			return "", remaining
		}
		if s.stale(now) {
			delete(g.sources, source)
		}
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
		delete(g.sources, source)
		return matched, 0
	}

	s := g.track(now, source)
	s.failures = min(s.failures+1, 10)
	s.last = now
	if s.failures >= 5 {
		delay := (30 * time.Second) << (s.failures - 5)
		s.until = now.Add(min(delay, 15*time.Minute))
	}
	return "", 0
}
