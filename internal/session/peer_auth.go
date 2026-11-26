package session

import (
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"sync/atomic"
	"time"

	"github.com/pion/webrtc/v4"

	"github.com/tangoui/kkdesk/internal/pairing"
)

const (
	metaAuth       byte = 0x41
	metaAuthResult byte = 0x52
	// metaViewerID carries a controller's identity claim before its
	// password proof: [0x49][public key][signature of the transcript]. Hosts
	// that do not know it drop it (unknown frames before authentication).
	metaViewerID    byte = 0x49
	viewerClaimSize      = 1 + ed25519.PublicKeySize + ed25519.SignatureSize
	authOK          byte = 0
	authWrong       byte = 1
	authLocked      byte = 2
	authWait             = 10 * time.Second
)

var ErrBadPassword = errors.New("密码错误")

var errAuthTimeout = errors.New("密码验证超时，请两边都升级到最新版")

type PasswordLockedError struct {
	Seconds uint32
}

func (e *PasswordLockedError) Error() string {
	return fmt.Sprintf("对方因多次密码错误暂停了连接，请 %d 秒后再试", e.Seconds)
}

type PasswordVerifier func(PasswordTranscript, []byte) (matched string, remaining time.Duration)

type peerPasswordAuth struct {
	frame    chan []byte
	received bool // only accessed by the meta channel callback
	verified atomic.Bool
	claim    atomic.Pointer[[]byte] // the controller's identity claim, unverified
}

func (p *Peer) requirePassword() {
	p.auth.Store(&peerPasswordAuth{frame: make(chan []byte, 1)})
}

func (p *Peer) passwordPending() bool {
	a := p.auth.Load()
	return a != nil && !a.verified.Load()
}

func (p *Peer) HostKey() string { return p.hostKey }

// ViewerKey returns, on the host, the controller key proved for this
// connection's transcript ("" if the controller sent none). It is set before
// the password verifier runs.
func (p *Peer) ViewerKey() string {
	if k := p.viewerKey.Load(); k != nil {
		return *k
	}
	return ""
}

// viewerClaim is a controller's identity claim for transcript.
func viewerClaim(id *pairing.Store, tx PasswordTranscript) []byte {
	claim := append([]byte{metaViewerID}, id.PublicKeyBytes()...)
	return append(claim, id.SignViewer(tx.bytes())...)
}

// verifyViewerClaim returns the encoded key of a valid claim for tx, else "".
func verifyViewerClaim(claim []byte, tx PasswordTranscript) string {
	if len(claim) != viewerClaimSize || claim[0] != metaViewerID {
		return ""
	}
	public, sig := claim[1:1+ed25519.PublicKeySize], claim[1+ed25519.PublicKeySize:]
	if !pairing.VerifyViewer(public, sig, tx.bytes()) {
		return ""
	}
	return pairing.Encode(public)
}

func (p *Peer) handlePasswordMeta(msg webrtc.DataChannelMessage) bool {
	a := p.auth.Load()
	b := msg.Data
	switch b[0] {
	case metaViewerID:
		if p.isHost && a != nil && !a.received && !msg.IsString && len(b) == viewerClaimSize {
			claim := append([]byte(nil), b...)
			a.claim.CompareAndSwap(nil, &claim)
		}
		return true
	case metaAuth, metaAuthResult:
		if a == nil || a.received || msg.IsString {
			return true
		}
		if p.isHost {
			if b[0] != metaAuth || len(b) != 1+sha256.Size {
				return true
			}
		} else {
			if b[0] != metaAuthResult || !validAuthResult(b) {
				return true
			}
			if b[1] == authOK {
				a.verified.Store(true)
			}
		}
		a.received = true
		a.frame <- append([]byte(nil), b[1:]...)
		return true
	case metaQuality:
		if p.passwordPending() {
			p.savePendingQuality(b)
			return true
		}
	}
	return p.passwordPending()
}

func validAuthResult(b []byte) bool {
	return (len(b) == 2 && (b[1] == authOK || b[1] == authWrong)) ||
		(len(b) == 6 && b[1] == authLocked && binary.BigEndian.Uint32(b[2:]) > 0)
}

func (p *Peer) sendMeta(frame []byte) error {
	p.mu.Lock()
	dc := p.dcMeta
	p.mu.Unlock()
	if dc == nil {
		return errors.New("session: meta channel not open")
	}
	return dc.Send(frame)
}

func (p *Peer) waitPasswordFrame(ctx context.Context) ([]byte, error) {
	frames := p.auth.Load().frame
	timer := time.NewTimer(authWait)
	defer timer.Stop()
	select {
	case b := <-frames:
		return b, nil
	case <-p.done:
		// The host sends the result and goodbye on the same ordered channel.
		select {
		case b := <-frames:
			return b, nil
		default:
		}
		if reason := p.Reason(); reason != "" {
			return nil, errors.New(reason)
		}
		return nil, errors.New("session: connection closed during authentication")
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-timer.C:
		return nil, errAuthTimeout
	}
}

// AcceptPassword waits for the WebRTC channels and verifies one proof.
func (p *Peer) AcceptPassword(ctx context.Context, tx PasswordTranscript, check PasswordVerifier) error {
	if err := p.waitReady(ctx); err != nil {
		return err
	}
	proof, err := p.waitPasswordFrame(ctx)
	if err != nil {
		p.CloseWithReason(err.Error())
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if claim := p.auth.Load().claim.Load(); claim != nil {
		if key := verifyViewerClaim(*claim, tx); key != "" {
			p.viewerKey.Store(&key)
		}
	}
	matched, remaining := check(tx, proof)
	switch {
	case remaining > 0:
		seconds := uint32((remaining + time.Second - 1) / time.Second)
		err = &PasswordLockedError{Seconds: seconds}
		_ = p.sendMeta(binary.BigEndian.AppendUint32([]byte{metaAuthResult, authLocked}, seconds))
	case matched == "":
		err = ErrBadPassword
		_ = p.sendMeta([]byte{metaAuthResult, authWrong})
	default:
		p.SetAuthPassword(matched)
		p.auth.Load().verified.Store(true)
		return p.sendMeta([]byte{metaAuthResult, authOK})
	}
	p.CloseWithReason(err.Error())
	return err
}

func (p *Peer) provePassword(ctx context.Context, tx PasswordTranscript, password string) error {
	if err := p.waitReady(ctx); err != nil {
		return err
	}
	if p.identity != nil {
		if err := p.sendMeta(viewerClaim(p.identity, tx)); err != nil {
			return err
		}
	}
	if err := p.sendMeta(append([]byte{metaAuth}, tx.Proof(password)...)); err != nil {
		return err
	}
	result, err := p.waitPasswordFrame(ctx)
	if err != nil {
		return err
	}
	switch result[0] {
	case authOK:
		go p.clockLoop()
		return nil
	case authWrong:
		return ErrBadPassword
	default:
		return &PasswordLockedError{Seconds: binary.BigEndian.Uint32(result[1:])}
	}
}
