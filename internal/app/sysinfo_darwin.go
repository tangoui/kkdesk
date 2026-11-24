//go:build darwin

package app

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"golang.org/x/sys/unix"
)

func platformInfo() []string {
	var out []string
	ver, _ := unix.Sysctl("kern.osproductversion")
	build, _ := unix.Sysctl("kern.osversion")
	out = append(out, fmt.Sprintf("os: macOS %s (%s)", ver, build))
	model, _ := unix.Sysctl("hw.model")
	cpu, _ := unix.Sysctl("machdep.cpu.brand_string")
	out = append(out, fmt.Sprintf("cpu: %s, model %s", cpu, model))
	if mem, err := unix.SysctlUint64("hw.memsize"); err == nil {
		out = append(out, fmt.Sprintf("memory: %.1f GiB", float64(mem)/(1<<30)))
	}
	return append(out, displayInfo()...)
}

// displayInfo summarizes system_profiler's GPU and display section.
func displayInfo() []string {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	b, err := exec.CommandContext(ctx, "system_profiler", "SPDisplaysDataType").Output()
	if err != nil {
		return []string{fmt.Sprintf("gpu: system_profiler failed: %v", err)}
	}
	var out []string
	for _, l := range strings.Split(string(b), "\n") {
		t := strings.TrimSpace(l)
		for _, k := range []string{"Chipset Model:", "Total Number of Cores:", "Metal Support:", "Resolution:", "UI Looks like:", "Main Display:", "Refresh Rate:"} {
			if strings.HasPrefix(t, k) {
				out = append(out, "display: "+t)
			}
		}
		if strings.HasSuffix(t, ":") && strings.HasPrefix(l, "        ") && !strings.HasPrefix(l, "          ") {
			out = append(out, "display: "+strings.TrimSuffix(t, ":")) // a display name
		}
	}
	return out
}
