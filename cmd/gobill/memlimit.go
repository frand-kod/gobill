package main

import (
	"bufio"
	"os"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"strconv"
	"strings"

	"github.com/frand-kod/gobill/internal/db"
)

// smallRAM is the memory budget under which the SQLite page cache shrinks (see tuneMemory).
const smallRAM = 256 << 20

// tuneMemory sizes the app to the memory it may use. Go does not read a container or cgroup limit
// itself, so without this the GC lets the heap grow past a 128 MB limit and the kernel kills the
// process. The soft limit makes the GC work harder near the budget instead. When GOMEMLIMIT is set,
// it wins. SQLite's page cache lives outside the Go heap, so on a small budget it shrinks too.
// It returns the budget in bytes (0 = unknown) and where it came from.
func tuneMemory() (int64, string) {
	budget, src := int64(0), ""
	if os.Getenv("GOMEMLIMIT") != "" {
		budget, src = debug.SetMemoryLimit(-1), "GOMEMLIMIT"
	} else if n := cgroupLimit(); n > 0 {
		// the remaining 30% is for SQLite, goroutine stacks and the binary
		budget, src = n*7/10, "cgroup"
		debug.SetMemoryLimit(budget)
	} else if n := memTotal(); n > 0 {
		// shared with the OS and anything else on the box
		budget, src = n/2, "system RAM"
		debug.SetMemoryLimit(budget)
	}
	if budget > 0 && budget <= smallRAM {
		db.CacheKiB = 2000
	}
	return budget, src
}

// minProcs makes sure one thread blocked in a syscall does not stall the whole app. On one core
// Go runs a single P; SQLite's fsync (5 ms on NVMe, tens of ms on SD/eMMC) then froze every
// goroutine, the RADIUS read loop included, and accounting topped out at a third of what the CPU
// could do. Two Ps share the one core and keep serving while the other thread waits on the disk.
// GOMAXPROCS from the environment wins.
func minProcs() {
	if os.Getenv("GOMAXPROCS") == "" && runtime.GOMAXPROCS(0) < 2 {
		runtime.GOMAXPROCS(2)
	}
}

func cgroupLimit() int64 { return cgroupLimitAt("/proc/self/cgroup", "/sys/fs/cgroup") }

// cgroupLimitAt returns the smallest memory.max (cgroup v2) or memory.limit_in_bytes (v1) on the
// path from the process's cgroup (listed in procFile) up to the root of sys, or 0 when there is none.
func cgroupLimitAt(procFile, sys string) int64 {
	b, err := os.ReadFile(procFile)
	if err != nil {
		return 0
	}
	var best int64
	take := func(file string) {
		v, err := os.ReadFile(file)
		if err != nil {
			return
		}
		// "max" (v2) or a huge number (v1) mean no limit
		n, err := strconv.ParseInt(strings.TrimSpace(string(v)), 10, 64)
		if err == nil && n > 0 && n < 1<<50 && (best == 0 || n < best) {
			best = n
		}
	}
	for _, line := range strings.Split(strings.TrimSpace(string(b)), "\n") {
		parts := strings.SplitN(line, ":", 3)
		if len(parts) != 3 {
			continue
		}
		root, file := sys, "memory.max"
		if parts[0] != "0" { // v1: only the memory controller
			if !strings.Contains(","+parts[1]+",", ",memory,") {
				continue
			}
			root, file = filepath.Join(sys, "memory"), "memory.limit_in_bytes"
		}
		for p := filepath.Clean("/" + parts[2]); ; p = filepath.Dir(p) {
			take(filepath.Join(root, p, file))
			if p == "/" {
				break
			}
		}
	}
	return best
}

// memTotal is MemTotal from /proc/meminfo in bytes, or 0.
func memTotal() int64 {
	f, err := os.Open("/proc/meminfo")
	if err != nil {
		return 0
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		if f := strings.Fields(sc.Text()); len(f) >= 2 && f[0] == "MemTotal:" {
			kb, _ := strconv.ParseInt(f[1], 10, 64)
			return kb << 10
		}
	}
	return 0
}
