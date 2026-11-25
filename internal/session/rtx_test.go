package session

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/pion/webrtc/v4"
)

func TestHostNegotiatesH264RTX(t *testing.T) {
	for _, offerRTX := range []bool{true, false} {
		name := "without RTX"
		if offerRTX {
			name = "with RTX"
		}
		t.Run(name, func(t *testing.T) {
			media := &webrtc.MediaEngine{}
			video := webrtc.RTPCodecTypeVideo
			// Offer a different payload mapping from the host's defaults.
			if err := media.RegisterCodec(webrtc.RTPCodecParameters{
				RTPCodecCapability: webrtc.RTPCodecCapability{
					MimeType: webrtc.MimeTypeH264, ClockRate: videoClock, SDPFmtpLine: h264Fmtp,
				},
				PayloadType: 98,
			}, video); err != nil {
				t.Fatal(err)
			}
			if offerRTX {
				if err := media.RegisterCodec(webrtc.RTPCodecParameters{
					RTPCodecCapability: webrtc.RTPCodecCapability{
						MimeType: webrtc.MimeTypeRTX, ClockRate: videoClock, SDPFmtpLine: "apt=98",
					},
					PayloadType: 99,
				}, video); err != nil {
					t.Fatal(err)
				}
			}
			var settings webrtc.SettingEngine
			settings.SetIncludeLoopbackCandidate(true)
			client, err := webrtc.NewAPI(webrtc.WithMediaEngine(media), webrtc.WithSettingEngine(settings)).
				NewPeerConnection(webrtc.Configuration{})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = client.Close() })
			if _, err := client.AddTransceiverFromKind(video, webrtc.RTPTransceiverInit{
				Direction: webrtc.RTPTransceiverDirectionRecvonly,
			}); err != nil {
				t.Fatal(err)
			}
			offer, err := client.CreateOffer(nil)
			if err != nil {
				t.Fatal(err)
			}
			gathered := webrtc.GatheringCompletePromise(client)
			if err := client.SetLocalDescription(offer); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			select {
			case <-gathered:
			case <-ctx.Done():
				t.Fatal("receiver did not gather ICE")
			}
			host, err := newPeer(true, true, ModeDesktop)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = host.Close() })
			answer, err := host.answer(*client.LocalDescription())
			if err != nil {
				t.Fatal(err)
			}
			for _, attr := range []string{"a=rtpmap:99 rtx/90000", "a=fmtp:99 apt=98", "a=ssrc-group:FID "} {
				if strings.Contains(answer.SDP, attr) != offerRTX {
					t.Errorf("answer should advertise %q = %t:\n%s", attr, offerRTX, answer.SDP)
				}
			}
		})
	}
}
