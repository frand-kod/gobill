package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestCgroupLimitAt(t *testing.T) {
	dir := t.TempDir()
	write := func(rel, v string) {
		p := filepath.Join(dir, rel)
		os.MkdirAll(filepath.Dir(p), 0o755)
		os.WriteFile(p, []byte(v+"\n"), 0o644)
	}
	proc := filepath.Join(dir, "cgroup")
	sys := filepath.Join(dir, "sys")

	// v2: the limit sits on a parent slice, the own scope says "max"
	write("cgroup", "0::/box.slice/app.scope")
	write("sys/box.slice/app.scope/memory.max", "max")
	write("sys/box.slice/memory.max", "134217728")
	if n := cgroupLimitAt(proc, sys); n != 128<<20 {
		t.Fatalf("v2 parent limit: %d", n)
	}
	// the smaller of two limits wins
	write("sys/box.slice/app.scope/memory.max", "67108864")
	if n := cgroupLimitAt(proc, sys); n != 64<<20 {
		t.Fatalf("v2 smallest: %d", n)
	}
	// v1: the memory controller line; the huge value means unlimited
	write("cgroup", "4:cpu,cpuacct:/x\n7:memory:/docker/abc")
	write("sys/memory/docker/abc/memory.limit_in_bytes", "268435456")
	write("sys/memory/memory.limit_in_bytes", "9223372036854771712")
	if n := cgroupLimitAt(proc, sys); n != 256<<20 {
		t.Fatalf("v1: %d", n)
	}
	// no limit anywhere
	write("cgroup", "0::/free")
	if n := cgroupLimitAt(proc, sys); n != 0 {
		t.Fatalf("unlimited: %d", n)
	}
}
