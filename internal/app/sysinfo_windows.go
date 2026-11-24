//go:build windows && cgo

package app

import (
	"fmt"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"

	"mmdesk/internal/winmedia"
)

var procGlobalMemoryStatusEx = syscall.NewLazyDLL("kernel32.dll").NewProc("GlobalMemoryStatusEx")

// memoryStatusEx is MEMORYSTATUSEX.
type memoryStatusEx struct {
	Length               uint32
	MemoryLoad           uint32
	TotalPhys, AvailPhys uint64
	TotalPageFile        uint64
	AvailPageFile        uint64
	TotalVirtual         uint64
	AvailVirtual         uint64
	AvailExtendedVirtual uint64
}

// gpuVendors names the PCI vendor ids seen on Windows PCs.
var gpuVendors = map[uint32]string{0x10DE: "NVIDIA", 0x1002: "AMD", 0x1022: "AMD", 0x8086: "Intel", 0x1414: "Microsoft",
	0x5143: "Qualcomm", 0x15AD: "VMware", 0x80EE: "VirtualBox", 0x1AB8: "Parallels", 0x1234: "QEMU"}

func platformInfo() []string {
	var out []string

	v := windows.RtlGetVersion()
	product, display, ubr := "", "", uint64(0)
	if k, err := registry.OpenKey(registry.LOCAL_MACHINE, `SOFTWARE\Microsoft\Windows NT\CurrentVersion`, registry.QUERY_VALUE); err == nil {
		product, _, _ = k.GetStringValue("ProductName")
		display, _, _ = k.GetStringValue("DisplayVersion")
		if display == "" {
			display, _, _ = k.GetStringValue("ReleaseId")
		}
		ubr, _, _ = k.GetIntegerValue("UBR")
		k.Close()
	}
	name := "Windows 10"
	if v.MajorVersion == 10 && v.BuildNumber >= 22000 {
		name = "Windows 11" // ProductName still says "Windows 10" on 11
	}
	out = append(out, fmt.Sprintf("os: %s %s, version %d.%d.%d.%d (ProductName %q)", name, display,
		v.MajorVersion, v.MinorVersion, v.BuildNumber, ubr, product))
	if v.MajorVersion < 10 || (v.MajorVersion == 10 && v.BuildNumber < 17763) {
		out = append(out, "os: WARNING: older than Windows 10 1809 (build 17763), which mmdesk requires")
	}

	if k, err := registry.OpenKey(registry.LOCAL_MACHINE, `HARDWARE\DESCRIPTION\System\CentralProcessor\0`, registry.QUERY_VALUE); err == nil {
		cpu, _, _ := k.GetStringValue("ProcessorNameString")
		mhz, _, _ := k.GetIntegerValue("~MHz")
		k.Close()
		out = append(out, fmt.Sprintf("cpu: %s (~%d MHz)", cpu, mhz))
	}
	ms := memoryStatusEx{Length: uint32(unsafe.Sizeof(memoryStatusEx{}))}
	if r, _, _ := procGlobalMemoryStatusEx.Call(uintptr(unsafe.Pointer(&ms))); r != 0 {
		out = append(out, fmt.Sprintf("memory: %.1f GiB total, %.1f GiB available", gib(ms.TotalPhys), gib(ms.AvailPhys)))
	}
	if s := sessionInfo(); s != "" {
		out = append(out, s)
	}

	adapters, outputs, err := winmedia.EnumAdapters()
	if err != nil {
		return append(out, fmt.Sprintf("gpu: enumeration failed: %v", err))
	}
	for i, a := range adapters {
		vendor := gpuVendors[a.VendorID]
		if vendor == "" {
			vendor = "unknown vendor"
		}
		drv := a.DriverVersion()
		if drv == "" {
			drv = "unknown"
		}
		sw := ""
		if a.Software {
			sw = ", software adapter"
		}
		out = append(out, fmt.Sprintf("gpu %d: %s (%s, vendor 0x%04X device 0x%04X subsys 0x%08X rev %d), VRAM %d MiB, "+
			"shared %d MiB, driver %s, LUID 0x%X, %d desktop output(s)%s", i, a.Description, vendor, a.VendorID, a.DeviceID,
			a.SubSysID, a.Revision, a.DedicatedVideoMemory>>20, a.SharedSystemMemory>>20, drv, a.LUID, a.Outputs, sw))
	}
	if len(outputs) == 0 {
		out = append(out, "display: none attached to the desktop (remote desktop session or headless?)")
	}
	for _, o := range outputs {
		prim := ""
		if o.Primary {
			prim = " PRIMARY"
		}
		scale := "unknown"
		if o.DPIX > 0 {
			scale = fmt.Sprintf("%d%% (%d dpi)", o.DPIX*100/96, o.DPIX)
		}
		out = append(out, fmt.Sprintf("display %s%s: gpu %d, mode %dx%d @ %d Hz %d bpp, desktop (%d,%d)-(%d,%d), rotation %d, scale %s",
			o.DeviceName, prim, o.Adapter, o.ModeWidth, o.ModeHeight, o.RefreshHz, o.BitsPerPixel,
			o.Left, o.Top, o.Right, o.Bottom, o.Rotation, scale))
	}
	return out
}

// sessionInfo reports a Remote Desktop session (no hardware encoder, and
// duplication shows the remote session), via GetSystemMetrics(SM_REMOTESESSION).
func sessionInfo() string {
	r, _, _ := syscall.NewLazyDLL("user32.dll").NewProc("GetSystemMetrics").Call(0x1000) // SM_REMOTESESSION
	if r != 0 {
		return "session: Remote Desktop session (capture and hardware encoding may not work)"
	}
	return ""
}

func gib(b uint64) float64 { return float64(b) / (1 << 30) }
