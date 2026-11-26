package session

import (
	"crypto/hmac"
	"crypto/sha256"
	"errors"
	"fmt"
	"net/netip"
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

// PasswordGate rate-limits password attempts per source: a LAN address (an
// IPv6 /64, see SourceKey), or all cross-network requests together. A source
// that keeps failing pauses only itself, so a stranger cannot lock everyone
// else out. Failures halve after each quiet spell, and Reset (a new
// password) lifts every pause.
//
// Many addresses must not multiply an attacker's guesses either: records
// that are paused or failed recently are never evicted (new sources share
// one overflow record once the table is full), and past globalFailBudget
// failures an hour every source that has failed is paused, while sources
// that never failed still get in.
type PasswordGate struct {
	mu       sync.Mutex
	sources  map[string]*gateSource
	overflow gateSource  // shared by new sources while the table is full
	recent   []time.Time // failures within the last hour, oldest first
}

type gateSource struct {
	failures int
	until    time.Time // paused until
	last     time.Time // latest failure (or decay step)
}

const (
	// gateForget is the quiet spell after which a source's failures halve.
	gateForget = 15 * time.Minute
	// maxGateSources bounds the sources remembered at once.
	maxGateSources = 256
	// globalFailBudget is how many failures an hour (all sources together)
	// pass before every source that has failed is paused.
	globalFailBudget = 60
	globalWindow     = time.Hour
)

// SourceKey is the gate source of a LAN client address: the IPv4 address,
// or the /64 prefix of an IPv6 address (one host can have any address in
// its /64).
func SourceKey(ip string) string {
	addr, err := netip.ParseAddr(ip)
	if err != nil {
		return "lan:" + ip
	}
	addr = addr.Unmap()
	if addr.Is4() {
		return "lan:" + addr.String()
	}
	prefix, _ := addr.Prefix(64)
	return "lan:" + prefix.String()
}

// Remaining returns the longest pause still running for any source.
func (g *PasswordGate) Remaining(now time.Time) time.Duration {
	g.mu.Lock()
	defer g.mu.Unlock()
	longest := g.overflow.until.Sub(now)
	for _, s := range g.sources {
		longest = max(longest, s.until.Sub(now))
	}
	return max(0, longest)
}

// Reset forgets every failure and lifts every pause (the password changed,
// so earlier guesses no longer matter).
func (g *PasswordGate) Reset() {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.sources, g.overflow, g.recent = nil, gateSource{}, nil
}

func (g *PasswordGate) sourceCount() int {
	g.mu.Lock()
	defer g.mu.Unlock()
	return len(g.sources)
}

// decay halves the failures once per quiet gateForget after the latest
// failure and after any pause (time spent paused is not quiet time).
func (s *gateSource) decay(now time.Time) {
	if s.until.After(s.last) {
		if now.Before(s.until) {
			return
		}
		s.last = s.until
	}
	for s.failures > 0 && now.Sub(s.last) >= gateForget {
		s.failures /= 2
		s.last = s.last.Add(gateForget)
	}
}

// record returns source's record, or nil if it has none. g.mu is held.
func (g *PasswordGate) record(now time.Time, source string) *gateSource {
	s := g.sources[source]
	if s == nil {
		return nil
	}
	s.decay(now)
	if s.failures == 0 && !now.Before(s.until) {
		delete(g.sources, source)
		return nil
	}
	return s
}

// track returns the record to count a failure of source in. g.mu is held.
func (g *PasswordGate) track(now time.Time, source string) *gateSource {
	if s := g.sources[source]; s != nil {
		return s
	}
	if g.sources == nil {
		g.sources = map[string]*gateSource{}
	}
	if len(g.sources) >= maxGateSources {
		for k := range g.sources {
			g.record(now, k) // drops records that have faded out
		}
	}
	if len(g.sources) >= maxGateSources {
		g.overflow.decay(now)
		return &g.overflow
	}
	s := &gateSource{}
	g.sources[source] = s
	return s
}

// recentFailures drops failures older than the window and counts the rest.
// g.mu is held.
func (g *PasswordGate) recentFailures(now time.Time) int {
	i := 0
	for i < len(g.recent) && now.Sub(g.recent[i]) >= globalWindow {
		i++
	}
	g.recent = g.recent[i:]
	return len(g.recent)
}

// Check returns the matched password, or the time until source's pause ends.
// Two zero results represent an incorrect password.
func (g *PasswordGate) Check(now time.Time, source string, transcript PasswordTranscript, proof []byte, temporary, fixed string) (string, time.Duration) {
	g.mu.Lock()
	defer g.mu.Unlock()
	s := g.record(now, source)
	if s == nil && len(g.sources) >= maxGateSources {
		g.overflow.decay(now)
		if g.overflow.failures > 0 || now.Before(g.overflow.until) {
			s = &g.overflow
		}
	}
	if s != nil {
		if remaining := s.until.Sub(now); remaining > 0 {
			return "", remaining
		}
		if g.recentFailures(now) >= globalFailBudget {
			// Too many failures overall: sources that have failed wait for
			// the oldest of them to leave the window.
			return "", g.recent[0].Add(globalWindow).Sub(now)
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
		if s != nil && s != &g.overflow {
			delete(g.sources, source)
		}
		return matched, 0
	}

	s = g.track(now, source)
	s.failures = min(s.failures+1, 10)
	s.last = now
	if s.failures >= 5 {
		delay := (30 * time.Second) << (s.failures - 5)
		s.until = now.Add(min(delay, 15*time.Minute))
	}
	g.recentFailures(now)
	if len(g.recent) < 4*globalFailBudget { // bounded; only the count matters
		g.recent = append(g.recent, now)
	}
	return "", 0
}
