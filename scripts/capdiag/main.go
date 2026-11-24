//go:build darwin && cgo

// Command capdiag captures the main display for a few seconds and prints
// per-frame timing: SCK frame status counts, delivery interval, SCK display
// time vs callback arrival, and encode-call→output latency measured from
// arrival and from display time. WAITDISP=1 delays each Encode call until
// the frame's display time. Run it next to scripts/anim.
package main

import (
	"flag"
	"fmt"
	"os"
	"sort"
	"sync"
	"time"

	"github.com/tangoui/kkdesk/internal/capture"
	"github.com/tangoui/kkdesk/internal/codec"
)

func med(d []time.Duration) time.Duration {
	if len(d) == 0 {
		return 0
	}
	s := append([]time.Duration(nil), d...)
	sort.Slice(s, func(i, j int) bool { return s[i] < s[j] })
	return s[len(s)/2]
}

func main() {
	secs := flag.Int("secs", 5, "seconds")
	fps := flag.Int("fps", 60, "fps")
	maxw := flag.Int("maxwidth", 1920, "max width")
	excl := flag.String("exclude", "kkdesk", "app whose windows are excluded")
	flag.Parse()
	var mu sync.Mutex
	var dispMinusArr, encLat, capEnc []time.Duration
	var arrivals []time.Time
	callAt := map[int64]time.Time{}
	dispOf := map[int64]time.Time{}
	var dispEnc, arrEncP []time.Duration
	var enc codec.Encoder
	s, err := capture.Start(capture.Options{FPS: *fps, MaxWidth: *maxw, ExcludeApp: *excl}, func(f capture.Frame) {
		mu.Lock()
		arrivals = append(arrivals, f.ArrivedAt)
		if !f.DisplayAt.IsZero() {
			dispMinusArr = append(dispMinusArr, f.DisplayAt.Sub(f.ArrivedAt))
		}
		if enc == nil {
			var err error
			enc, err = codec.NewEncoder(f.Width, f.Height, *fps, 8_000_000, func(nal []byte, c time.Time, key bool) {
				now := time.Now()
				mu.Lock()
				if t, ok := callAt[c.UnixNano()]; ok {
					encLat = append(encLat, now.Sub(t))
					capEnc = append(capEnc, now.Sub(c))
					if d := dispOf[c.UnixNano()]; !d.IsZero() {
						dispEnc = append(dispEnc, now.Sub(d))
						arrEncP = append(arrEncP, now.Sub(c))
					}
				}
				mu.Unlock()
			})
			if err != nil {
				panic(err)
			}
		}
		e := enc
		if os.Getenv("WAITDISP") != "" && !f.DisplayAt.IsZero() {
			mu.Unlock()
			time.Sleep(time.Until(f.DisplayAt))
			mu.Lock()
		}
		callAt[f.ArrivedAt.UnixNano()] = time.Now()
		dispOf[f.ArrivedAt.UnixNano()] = f.DisplayAt
		mu.Unlock()
		e.Encode(codec.Frame{Pixels: f.Pixels, CaptureAt: f.ArrivedAt}, false)
	})
	if err != nil {
		panic(err)
	}
	time.Sleep(2 * time.Second) // warm-up, not counted
	mu.Lock()
	arrivals, dispMinusArr, encLat, capEnc, dispEnc, arrEncP = nil, nil, nil, nil, nil, nil
	mu.Unlock()
	time.Sleep(time.Duration(*secs) * time.Second)
	st := s.Stats()
	s.Close()
	fmt.Printf("sck stats %+v\n", st)
	mu.Lock()
	e := enc
	mu.Unlock()
	if e != nil {
		e.Close()
	}
	mu.Lock()
	defer mu.Unlock()
	var iv []time.Duration
	for i := 1; i < len(arrivals); i++ {
		iv = append(iv, arrivals[i].Sub(arrivals[i-1]))
	}
	long := 0
	for _, d := range iv {
		if d > 25*time.Millisecond {
			long++
		}
	}
	fmt.Printf("intervals>25ms=%d of %d\n", long, len(iv))
	fmt.Printf("frames=%d fps=%.1f interval_med=%v\n", len(arrivals), float64(len(arrivals))/float64(*secs), med(iv))
	fmt.Printf("encode call->output med=%v  arrival->output med=%v (n=%d)\n", med(encLat), med(capEnc), len(encLat))
	pr := func(name string, d []time.Duration) {
		s := append([]time.Duration(nil), d...)
		sort.Slice(s, func(i, j int) bool { return s[i] < s[j] })
		if len(s) > 0 {
			fmt.Printf("%s p5=%v p50=%v p95=%v\n", name, s[len(s)*5/100], s[len(s)/2], s[len(s)*95/100])
		}
	}
	pr("display->output", dispEnc)
	pr("arrival->output", arrEncP)
	pr("display-arrival", dispMinusArr)
}
