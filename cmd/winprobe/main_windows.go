// winprobe measures the real application process with Windows APIs, not Go heap size.
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"syscall"
	"time"
	"unsafe"
)

type counters struct {
	Size, Faults                                                                                               uint32
	PeakWorkingSet, WorkingSet, PeakPaged, Paged, PeakNonPaged, NonPaged, Pagefile, PeakPagefile, PrivateUsage uintptr
}

func main() {
	u := syscall.NewLazyDLL("user32.dll")
	k := syscall.NewLazyDLL("kernel32.dll")
	class, _ := syscall.UTF16PtrFromString("PocketHTTPNativeWindow")
	hwnd, _, _ := u.NewProc("FindWindowW").Call(uintptr(unsafe.Pointer(class)), 0)
	if hwnd == 0 {
		fmt.Fprintln(os.Stderr, "No native Pocket HTTP window found")
		os.Exit(1)
	}
	var pid uint32
	u.NewProc("GetWindowThreadProcessId").Call(hwnd, uintptr(unsafe.Pointer(&pid)))
	p, _, err := k.NewProc("OpenProcess").Call(0x410, 0, uintptr(pid))
	if p == 0 {
		panic(err)
	}
	defer k.NewProc("CloseHandle").Call(p)
	var c counters
	c.Size = uint32(unsafe.Sizeof(c))
	ok, _, err := syscall.NewLazyDLL("psapi.dll").NewProc("GetProcessMemoryInfo").Call(p, uintptr(unsafe.Pointer(&c)), uintptr(c.Size))
	if ok == 0 {
		panic(err)
	}
	if len(os.Args) > 2 && os.Args[1] == "--stress" {
		count, _ := strconv.Atoi(os.Args[2])
		send := u.NewProc("SendMessageW")
		button, _, _ := u.NewProc("GetDlgItem").Call(hwnd, 1013)
		for i := 0; i < count; i++ {
			send.Call(hwnd, 0x111, 1013, 0)
			deadline := time.Now().Add(35 * time.Second)
			for {
				enabled, _, _ := u.NewProc("IsWindowEnabled").Call(button)
				if enabled != 0 {
					break
				}
				if time.Now().After(deadline) {
					panic("request did not finish")
				}
				time.Sleep(5 * time.Millisecond)
			}
		}
		syscall.NewLazyDLL("psapi.dll").NewProc("GetProcessMemoryInfo").Call(p, uintptr(unsafe.Pointer(&c)), uintptr(c.Size))
	}
	json.NewEncoder(os.Stdout).Encode(map[string]any{"pid": pid, "working_set_MiB": float64(c.WorkingSet) / 1048576, "peak_working_set_MiB": float64(c.PeakWorkingSet) / 1048576, "private_usage_MiB": float64(c.PrivateUsage) / 1048576, "note": "Windows process counters; Wine values are not real-Windows measurements"})
	if len(os.Args) > 1 && os.Args[1] == "--close" {
		u.NewProc("SendMessageW").Call(hwnd, 0x10, 0, 0)
	}
}
