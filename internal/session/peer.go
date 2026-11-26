package session

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"math/rand/v2"
	"net"
	"sync"
	"sync/atomic"
	"time"

	"github.com/pion/ice/v4"
	"github.com/pion/interceptor"
	"github.com/pion/rtcp"
	"github.com/pion/rtp"
	"github.com/pion/rtp/codecs"
	"github.com/pion/webrtc/v4"
	"golang.org/x/net/proxy"

	"github.com/tangoui/kkdesk/internal/input"
)

const (
	// h264Fmtp is Constrained Baseline 3.1, non-interleaved packetization.
	h264Fmtp    = "level-asymmetry-allowed=1;packetization-mode=1;profile-level-id=42e01f"
	h264PT      = 102
	h264RTXPT   = 103
	videoClock  = 90000
	rtpMTU      = 1200
	readyWait   = 15 * time.Second
	gatherWait  = 10 * time.Second
	metaMaxWait = 300 * time.Millisecond // deliver a frame without captureAt after this
	autoPLIGap  = 500 * time.Millisecond
	reasonWait  = 150 * time.Millisecond

	labelMove  = "input-move"
	labelInput = "input"
	labelMeta  = "meta"
	labelFiles = "files"
	numLabels  = 4 // data channels that must open before a session is ready

	metaCapture byte = 'c' // 'c' u32 rtpTimestamp, i64 captureUnixNano (big endian)
	metaBye     byte = 'b' // 'b' [reason UTF-8]
	metaQuality byte = 'q' // 'q' u32 bitrateBps, u16 fps (big endian)
	// metaPing and metaPong (clock sync) are defined in clock.go.
)

// ErrNotHost is returned by WriteVideo on a controller-side peer.
var ErrNotHost = errors.New("session: video can only be sent by the host")

// ErrNoVideo is returned by WriteVideo on a files-only (ModeFiles) session.
var ErrNoVideo = errors.New("session: files-only session has no video")

// Stats is a snapshot of transport counters. Bytes count H.264 payload bytes
// (Annex-B access units) handed to or delivered by the peer.
type Stats struct {
	FramesSent, BytesSent         uint64
	FramesReceived, BytesReceived uint64
	FramesDropped                 uint64 // incomplete frames discarded by the receiver
	PacketsSent, PacketsReceived  uint64 // RTP video packets (pion stats)
	PacketsLost                   int64  // receiver: inbound loss; host: as reported by the remote
}

// Peer is one end of an established session. The host sends video and
// receives keyframe requests; the controller receives video. Both sides can
// send and receive input events. Peer is safe for concurrent use.
type Peer struct {
	pc     *webrtc.PeerConnection
	isHost bool
	mode   Mode

	// host video sending
	writeMu   sync.Mutex
	track     *webrtc.TrackLocalStaticRTP
	payloader codecs.H264Payloader
	seq       uint16
	tsBase    uint32
	lastTS    uint32
	t0        time.Time
	started   bool

	mu                    sync.Mutex
	onVideo               func([]byte, time.Time)
	onInput               func(input.Event)
	onKeyframe            func()
	onQuality             func(Quality)
	accessibilityDenied   bool
	onAccessibilityChange func()
	// onClipboard receives clipboard text; clip joins its chunks (meta
	// channel callback only).
	onClipboard func(string)
	clip        clipAssembler
	// pendingQuality is the latest request received before OnQuality.
	pendingQuality *Quality
	reason         string // close reason sent by the other side
	remote         string // remote signaling address
	authPassword   string // the host password the controller proved, "" if none
	dcMove         *webrtc.DataChannel
	dcInput        *webrtc.DataChannel
	dcMeta         *webrtc.DataChannel
	files          *fileChannel
	lastPLI        time.Time

	opened    atomic.Int32
	ready     chan struct{}
	readyOnce sync.Once

	done      chan struct{}
	doneOnce  sync.Once
	closeOnce sync.Once
	closeErr  error

	videoEv    chan videoEvent
	remoteSSRC atomic.Uint32

	clock ClockSync // controller: host clock offset estimate (clock.go)

	framesSent, bytesSent, framesRecv, bytesRecv, framesDropped atomic.Uint64
}

type videoEvent struct {
	frame  []byte
	ts     uint32
	isMeta bool
	nano   int64
}

func newPeer(isHost, loopback bool, mode Mode) (*Peer, error) {
	return newConfiguredPeer(isHost, loopback, mode, webrtc.Configuration{}, nil)
}

func newConfiguredPeer(isHost, loopback bool, mode Mode, config webrtc.Configuration, turnDialer proxy.Dialer) (*Peer, error) {
	m := &webrtc.MediaEngine{}
	fb := []webrtc.RTCPFeedback{{Type: "goog-remb"}, {Type: "ccm", Parameter: "fir"}, {Type: "nack"}, {Type: "nack", Parameter: "pli"}}
	if err := m.RegisterCodec(webrtc.RTPCodecParameters{
		RTPCodecCapability: webrtc.RTPCodecCapability{MimeType: webrtc.MimeTypeH264, ClockRate: videoClock, SDPFmtpLine: h264Fmtp, RTCPFeedback: fb},
		PayloadType:        h264PT,
	}, webrtc.RTPCodecTypeVideo); err != nil {
		return nil, err
	}
	if isHost {
		// The default NACK responder uses a dedicated retransmission stream
		// when the receiver offers RTX for this H.264 codec.
		if err := m.RegisterCodec(webrtc.RTPCodecParameters{
			RTPCodecCapability: webrtc.RTPCodecCapability{
				MimeType:    webrtc.MimeTypeRTX,
				ClockRate:   videoClock,
				SDPFmtpLine: fmt.Sprintf("apt=%d", h264PT),
			},
			PayloadType: h264RTXPT,
		}, webrtc.RTPCodecTypeVideo); err != nil {
			return nil, err
		}
	}
	ir := &interceptor.Registry{}
	if err := webrtc.RegisterDefaultInterceptors(m, ir); err != nil {
		return nil, err
	}

	se := webrtc.SettingEngine{}
	networkTypes := []webrtc.NetworkType{webrtc.NetworkTypeUDP4}
	if len(config.ICEServers) != 0 {
		networkTypes = append(networkTypes, webrtc.NetworkTypeTCP4)
	}
	se.SetNetworkTypes(networkTypes)
	if turnDialer != nil {
		se.SetICEProxyDialer(turnDialer)
	}
	se.SetICEMulticastDNSMode(ice.MulticastDNSModeDisabled)
	se.SetIncludeLoopbackCandidate(len(config.ICEServers) == 0)
	if loopback {
		// Self-connection: only offer 127.0.0.1 so ICE is fast and deterministic.
		se.SetIPFilter(func(ip net.IP) bool { return ip.IsLoopback() })
	}
	se.SetICETimeouts(3*time.Second, 6*time.Second, time.Second)

	api := webrtc.NewAPI(webrtc.WithMediaEngine(m), webrtc.WithInterceptorRegistry(ir), webrtc.WithSettingEngine(se))
	pc, err := api.NewPeerConnection(config)
	if err != nil {
		return nil, err
	}
	p := &Peer{
		pc:      pc,
		isHost:  isHost,
		mode:    normMode(mode),
		ready:   make(chan struct{}),
		done:    make(chan struct{}),
		videoEv: make(chan videoEvent, 64),
		tsBase:  rand.Uint32(),
		seq:     uint16(rand.Uint32()),
	}
	pc.OnConnectionStateChange(func(s webrtc.PeerConnectionState) {
		switch s {
		case webrtc.PeerConnectionStateDisconnected, webrtc.PeerConnectionStateFailed, webrtc.PeerConnectionStateClosed:
			p.shutdown()
		}
	})
	if isHost {
		pc.OnDataChannel(p.attach)
	} else {
		pc.OnTrack(p.readTrack)
		go p.videoLoop()
	}
	return p, nil
}

// attach registers a data channel by label (host: from OnDataChannel;
// controller: right after creating it).
func (p *Peer) attach(dc *webrtc.DataChannel) {
	p.mu.Lock()
	switch dc.Label() {
	case labelMove:
		p.dcMove = dc
	case labelInput:
		p.dcInput = dc
	case labelMeta:
		p.dcMeta = dc
	case labelFiles:
		fc := newFileChannel(dc)
		p.files = fc
		p.mu.Unlock()
		dc.OnOpen(p.channelOpened)
		dc.OnClose(func() { fc.markClosed(); p.shutdown() })
		go func() { <-p.done; fc.markClosed() }()
		return
	default:
		p.mu.Unlock()
		return
	}
	p.mu.Unlock()

	dc.OnOpen(p.channelOpened)
	dc.OnClose(p.shutdown)
	if dc.Label() == labelMeta {
		dc.OnMessage(p.handleMeta)
		return
	}
	dc.OnMessage(func(msg webrtc.DataChannelMessage) {
		e, err := input.Unmarshal(msg.Data)
		if err != nil {
			return
		}
		p.mu.Lock()
		f := p.onInput
		p.mu.Unlock()
		if f != nil {
			f(e)
		}
	})
}

// channelOpened marks the session ready once every data channel is open.
func (p *Peer) channelOpened() {
	if p.opened.Add(1) == numLabels {
		p.readyOnce.Do(func() {
			close(p.ready)
			if !p.isHost {
				go p.clockLoop()
			}
		})
	}
}

func (p *Peer) handleMeta(msg webrtc.DataChannelMessage) {
	b := msg.Data
	if len(b) == 0 {
		return
	}
	switch b[0] {
	case metaBye:
		p.mu.Lock()
		p.reason = string(b[1:])
		p.mu.Unlock()
		p.shutdown()
	case metaQuality:
		p.handleQuality(b)
	case metaClip:
		p.handleClip(b)
	case metaPing, metaPong:
		p.handleClock(b)
	case metaAccessibility:
		p.handleAccessibility(msg)
	case metaCapture:
		if len(b) != 13 || p.isHost {
			return
		}
		ev := videoEvent{isMeta: true, ts: binary.BigEndian.Uint32(b[1:]), nano: int64(binary.BigEndian.Uint64(b[5:]))}
		select {
		case p.videoEv <- ev:
		case <-p.done:
		}
	}
}

// offer prepares the controller side and returns the complete offer.
func (p *Peer) offer() (*webrtc.SessionDescription, error) {
	var zero uint16
	unordered := false
	for _, c := range []struct {
		label string
		init  *webrtc.DataChannelInit
	}{
		{labelMove, &webrtc.DataChannelInit{Ordered: &unordered, MaxRetransmits: &zero}},
		{labelInput, nil},
		{labelMeta, nil},
		{labelFiles, nil}, // ordered and reliable
	} {
		dc, err := p.pc.CreateDataChannel(c.label, c.init)
		if err != nil {
			return nil, err
		}
		p.attach(dc)
	}
	if p.mode != ModeFiles {
		if _, err := p.pc.AddTransceiverFromKind(webrtc.RTPCodecTypeVideo,
			webrtc.RTPTransceiverInit{Direction: webrtc.RTPTransceiverDirectionRecvonly}); err != nil {
			return nil, err
		}
	}
	o, err := p.pc.CreateOffer(nil)
	if err != nil {
		return nil, err
	}
	return p.setLocalAndGather(o)
}

// answer applies the controller's offer on the host side and returns the
// complete answer.
func (p *Peer) answer(offer webrtc.SessionDescription) (*webrtc.SessionDescription, error) {
	if err := p.pc.SetRemoteDescription(offer); err != nil {
		return nil, err
	}
	if p.mode == ModeFiles {
		a, err := p.pc.CreateAnswer(nil)
		if err != nil {
			return nil, err
		}
		return p.setLocalAndGather(a)
	}
	track, err := webrtc.NewTrackLocalStaticRTP(webrtc.RTPCodecCapability{
		MimeType: webrtc.MimeTypeH264, ClockRate: videoClock, SDPFmtpLine: h264Fmtp,
	}, "video", "kkdesk")
	if err != nil {
		return nil, err
	}
	sender, err := p.pc.AddTrack(track)
	if err != nil {
		return nil, err
	}
	p.track = track
	go p.readRTCP(sender)
	a, err := p.pc.CreateAnswer(nil)
	if err != nil {
		return nil, err
	}
	return p.setLocalAndGather(a)
}

// setLocalAndGather sets the local description and waits for ICE gathering
// to finish (non-trickle signaling).
func (p *Peer) setLocalAndGather(d webrtc.SessionDescription) (*webrtc.SessionDescription, error) {
	gathered := webrtc.GatheringCompletePromise(p.pc)
	if err := p.pc.SetLocalDescription(d); err != nil {
		return nil, err
	}
	select {
	case <-gathered:
	case <-time.After(gatherWait):
		return nil, errors.New("session: ICE gathering timed out")
	}
	return p.pc.LocalDescription(), nil
}

func (p *Peer) waitReady(ctx context.Context) error {
	t := time.NewTimer(readyWait)
	defer t.Stop()
	select {
	case <-p.ready:
		return nil
	case <-p.done:
		return errors.New("session: connection closed during setup")
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return errors.New("session: timed out establishing connection")
	}
}

func (p *Peer) readRTCP(sender *webrtc.RTPSender) {
	for {
		pkts, _, err := sender.ReadRTCP()
		if err != nil {
			return
		}
		for _, pkt := range pkts {
			switch pkt.(type) {
			case *rtcp.PictureLossIndication, *rtcp.FullIntraRequest:
				p.mu.Lock()
				f := p.onKeyframe
				p.mu.Unlock()
				if f != nil {
					f()
				}
			}
		}
	}
}

// WriteVideo sends one H.264 access unit in Annex-B format (host only).
// captureAt travels with the frame and is reported by the receiver's OnVideo.
// keyframe is informational; SPS/PPS must be included by the caller.
func (p *Peer) WriteVideo(nal []byte, captureAt time.Time, keyframe bool) error {
	_ = keyframe
	if p.track == nil {
		if p.isHost {
			return ErrNoVideo
		}
		return ErrNotHost
	}
	select {
	case <-p.done:
		return net.ErrClosed
	default:
	}
	p.writeMu.Lock()
	defer p.writeMu.Unlock()

	// 90 kHz timestamp derived from captureAt, strictly increasing.
	if !p.started {
		p.started, p.t0 = true, captureAt
	}
	ts := p.tsBase + uint32(captureAt.Sub(p.t0).Nanoseconds()*9/100000)
	if p.framesSent.Load() > 0 && int32(ts-p.lastTS) <= 0 {
		ts = p.lastTS + 1
	}
	p.lastTS = ts

	p.mu.Lock()
	meta := p.dcMeta
	p.mu.Unlock()
	if meta != nil {
		var b [13]byte
		b[0] = metaCapture
		binary.BigEndian.PutUint32(b[1:], ts)
		binary.BigEndian.PutUint64(b[5:], uint64(captureAt.UnixNano()))
		_ = meta.Send(b[:])
	}

	payloads := p.payloader.Payload(rtpMTU, nal)
	for i, pl := range payloads {
		pkt := &rtp.Packet{
			Header: rtp.Header{
				Version:        2,
				PayloadType:    h264PT,
				SequenceNumber: p.seq,
				Timestamp:      ts,
				Marker:         i == len(payloads)-1,
			},
			Payload: pl,
		}
		p.seq++
		if err := p.track.WriteRTP(pkt); err != nil {
			return err
		}
	}
	p.framesSent.Add(1)
	p.bytesSent.Add(uint64(len(nal)))
	return nil
}

// readTrack reassembles access units from the remote video track.
func (p *Peer) readTrack(track *webrtc.TrackRemote, _ *webrtc.RTPReceiver) {
	p.remoteSSRC.Store(uint32(track.SSRC()))
	var a assembler
	for {
		pkt, _, err := track.ReadRTP()
		if err != nil {
			return
		}
		frame, ts, ok, lost := a.push(pkt)
		if lost {
			p.framesDropped.Add(1)
			p.autoKeyframe()
		}
		if !ok {
			continue
		}
		select {
		case p.videoEv <- videoEvent{frame: frame, ts: ts}:
		case <-p.done:
			return
		}
	}
}

// autoKeyframe requests a keyframe after packet loss, rate limited.
func (p *Peer) autoKeyframe() {
	p.mu.Lock()
	now := time.Now()
	if now.Sub(p.lastPLI) < autoPLIGap {
		p.mu.Unlock()
		return
	}
	p.lastPLI = now
	p.mu.Unlock()
	_ = p.RequestKeyframe()
}

// videoLoop pairs frames with their captureAt metadata and delivers them in
// order on a single goroutine.
func (p *Peer) videoLoop() {
	type pending struct {
		frame   []byte
		ts      uint32
		arrived time.Time
	}
	var queue []pending
	metas := map[uint32]int64{}
	var metaOrder []uint32
	tick := time.NewTicker(50 * time.Millisecond)
	defer tick.Stop()

	deliver := func(f pending, at time.Time) {
		p.framesRecv.Add(1)
		p.bytesRecv.Add(uint64(len(f.frame)))
		p.mu.Lock()
		cb := p.onVideo
		p.mu.Unlock()
		if cb != nil {
			cb(f.frame, at)
		}
	}
	for {
		select {
		case <-p.done:
			return
		case ev := <-p.videoEv:
			if ev.isMeta {
				metas[ev.ts] = ev.nano
				metaOrder = append(metaOrder, ev.ts)
				if len(metaOrder) > 512 {
					delete(metas, metaOrder[0])
					metaOrder = metaOrder[1:]
				}
			} else {
				queue = append(queue, pending{ev.frame, ev.ts, time.Now()})
			}
		case <-tick.C:
		}
		for len(queue) > 0 {
			h := queue[0]
			if n, ok := metas[h.ts]; ok {
				delete(metas, h.ts)
				deliver(h, time.Unix(0, n))
			} else if time.Since(h.arrived) > metaMaxWait {
				deliver(h, time.Time{})
			} else {
				break
			}
			queue = queue[1:]
		}
	}
}

// OnVideo sets the callback for reassembled Annex-B access units (controller).
// It is called from a single goroutine, in order. frame is owned by the callee.
// captureAt is zero if the frame's metadata did not arrive in time.
func (p *Peer) OnVideo(f func(frame []byte, captureAt time.Time)) {
	p.mu.Lock()
	p.onVideo = f
	p.mu.Unlock()
}

// SetAuthPassword records the host password the controller proved
// (Host.Accept sets it; the app sets it for cross-network peers).
func (p *Peer) SetAuthPassword(pw string) {
	p.mu.Lock()
	p.authPassword = pw
	p.mu.Unlock()
}

// AuthPassword returns the host password the controller proved, "" if it
// proved none (a paired phone, an older cross-network controller).
func (p *Peer) AuthPassword() string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.authPassword
}

// OnInput sets the callback for input events from the other side.
func (p *Peer) OnInput(f func(input.Event)) {
	p.mu.Lock()
	p.onInput = f
	p.mu.Unlock()
}

// OnKeyframeRequest sets the callback invoked when the controller asks for a
// keyframe (host).
func (p *Peer) OnKeyframeRequest(f func()) {
	p.mu.Lock()
	p.onKeyframe = f
	p.mu.Unlock()
}

// SendInput sends an input event: Move over the unordered lossy channel,
// everything else over the ordered reliable one.
func (p *Peer) SendInput(e input.Event) error {
	p.mu.Lock()
	dc := p.dcInput
	if e.Kind == input.Move {
		dc = p.dcMove
	}
	p.mu.Unlock()
	if dc == nil {
		return errors.New("session: input channel not open")
	}
	return dc.Send(e.Marshal(nil))
}

// RequestKeyframe sends an RTCP PLI for the received video (controller).
func (p *Peer) RequestKeyframe() error {
	ssrc := p.remoteSSRC.Load()
	if ssrc == 0 {
		for _, tr := range p.pc.GetTransceivers() {
			if r := tr.Receiver(); r != nil {
				for _, t := range r.Tracks() {
					if t.SSRC() != 0 {
						ssrc = uint32(t.SSRC())
					}
				}
			}
		}
	}
	if ssrc == 0 {
		return errors.New("session: no remote video track")
	}
	return p.pc.WriteRTCP([]rtcp.Packet{&rtcp.PictureLossIndication{MediaSSRC: ssrc}})
}

// Stats returns a snapshot of transport counters.
func (p *Peer) Stats() Stats {
	s := Stats{
		FramesSent:     p.framesSent.Load(),
		BytesSent:      p.bytesSent.Load(),
		FramesReceived: p.framesRecv.Load(),
		BytesReceived:  p.bytesRecv.Load(),
		FramesDropped:  p.framesDropped.Load(),
	}
	for _, v := range p.pc.GetStats() {
		switch st := v.(type) {
		case webrtc.InboundRTPStreamStats:
			s.PacketsReceived += uint64(st.PacketsReceived)
			s.PacketsLost += int64(st.PacketsLost)
		case webrtc.OutboundRTPStreamStats:
			s.PacketsSent += uint64(st.PacketsSent)
		case webrtc.RemoteInboundRTPStreamStats:
			s.PacketsLost += int64(st.PacketsLost)
		}
	}
	return s
}

// Done is closed when the connection fails or closes, or Close is called.
func (p *Peer) Done() <-chan struct{} { return p.done }

// shutdown marks the peer done and closes it in the background (safe to call
// from pion callbacks).
func (p *Peer) shutdown() {
	p.doneOnce.Do(func() { close(p.done) })
	go p.Close()
}

// Close tells the other side goodbye and tears down the connection. It is
// idempotent.
func (p *Peer) Close() error { return p.CloseWithReason("") }

// CloseWithReason is Close with a human-readable reason that the other side
// reads with Reason (for example a host rejecting the connection).
func (p *Peer) CloseWithReason(reason string) error {
	p.closeOnce.Do(func() {
		select {
		case <-p.done:
		default:
			p.mu.Lock()
			meta := p.dcMeta
			p.mu.Unlock()
			if meta != nil && meta.ReadyState() == webrtc.DataChannelStateOpen {
				if meta.Send(append([]byte{metaBye}, reason...)) == nil && reason != "" {
					// The other side may notice the closed connection before
					// the bye message; give the reason a moment to arrive.
					time.Sleep(reasonWait)
				}
			}
		}
		p.doneOnce.Do(func() { close(p.done) })
		if err := p.pc.Close(); err != nil {
			p.closeErr = fmt.Errorf("session: close: %w", err)
		}
	})
	return p.closeErr
}

// assembler turns RTP packets into Annex-B access units using the marker bit.
// Any sequence gap discards the frame in progress.
type assembler struct {
	depack  codecs.H264Packet
	buf     []byte
	ts      uint32
	lastSeq uint16
	haveSeq bool
	inFrame bool
	broken  bool
}

func (a *assembler) reset() {
	a.depack = codecs.H264Packet{}
	a.buf = nil
	a.inFrame = false
	a.broken = false
}

func (a *assembler) push(pkt *rtp.Packet) (frame []byte, ts uint32, ok, lost bool) {
	if a.haveSeq {
		d := pkt.SequenceNumber - a.lastSeq
		if d == 0 || d > 0x8000 {
			return nil, 0, false, false // duplicate or late (e.g. retransmission)
		}
		if d != 1 {
			// Packets went missing; the frame this packet belongs to may lack
			// its head, so skip it until its marker.
			lost = true
			a.reset()
			a.inFrame, a.broken, a.ts = true, true, pkt.Timestamp
		}
	}
	a.haveSeq, a.lastSeq = true, pkt.SequenceNumber

	if a.inFrame && pkt.Timestamp != a.ts {
		// New frame began without the previous marker.
		if !a.broken {
			lost = true
		}
		a.reset()
	}
	if !a.inFrame {
		a.inFrame, a.ts = true, pkt.Timestamp
	}
	if !a.broken && len(pkt.Payload) > 0 {
		out, err := a.depack.Unmarshal(pkt.Payload)
		if err != nil {
			a.broken, lost = true, true
		} else {
			a.buf = append(a.buf, out...)
		}
	}
	if !pkt.Marker {
		return nil, 0, false, lost
	}
	frame, ts, ok = a.buf, a.ts, !a.broken && len(a.buf) > 0
	a.reset()
	return frame, ts, ok, lost
}
