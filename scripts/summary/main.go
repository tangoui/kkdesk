// Command summary prints the stats.Summary of mmdesk JSON Lines logs.
//
//	go run ./scripts/summary host.jsonl view.jsonl
package main

import (
	"fmt"
	"os"
	"slices"
	"time"

	"mmdesk/internal/stats"
)

func ms(d time.Duration) string { return fmt.Sprintf("%.1f ms", float64(d)/float64(time.Millisecond)) }

func main() {
	for _, path := range os.Args[1:] {
		r, err := stats.Summary(path)
		if err != nil {
			fmt.Fprintln(os.Stderr, path+":", err)
			os.Exit(1)
		}
		fmt.Printf("== %s (%s): %d s, %d frames\n", path, r.Role, r.Seconds, r.Frames)
		fmt.Printf("latency p50 %s  p95 %s  fps %.1f  bitrate %.2f Mbps  loss %.2f%%\n",
			ms(r.LatencyP50), ms(r.LatencyP95), r.MeanFPS, r.MeanBitrateBps/1e6, r.MeanLossPct)
		var segs []string
		for k := range r.Segments {
			segs = append(segs, k)
		}
		slices.Sort(segs)
		for _, k := range segs {
			fmt.Printf("  segment %-22s %s\n", k, ms(r.Segments[k]))
		}
		var procs []string
		for k := range r.CPU {
			procs = append(procs, k)
		}
		slices.Sort(procs)
		total := 0.0
		for _, k := range procs {
			fmt.Printf("  cpu %-22s %.1f%%\n", k, r.CPU[k])
			total += r.CPU[k]
		}
		if len(procs) > 0 {
			fmt.Printf("  cpu %-22s %.1f%%\n", "total (self+system)", total)
		}
	}
}
