//go:build e2e && linux

package server

import (
	"bufio"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"testing"
)

// listeners returns the local addresses of every listening TCP and UDP socket of
// pid, read with `ss -ltunpH` (S1, S14).
func listeners(t *testing.T, pid int) []string {
	t.Helper()
	out, err := exec.Command("ss", "-ltunpH").Output()
	if err != nil {
		t.Fatalf("ss -ltunpH: %v", err)
	}
	t.Logf("ss -ltunpH lines of pid %d:", pid)
	var addrs []string
	for _, line := range strings.Split(string(out), "\n") {
		if !strings.Contains(line, fmt.Sprintf("pid=%d,", pid)) {
			continue
		}
		t.Logf("  %s", line)
		f := strings.Fields(line)
		if len(f) >= 5 {
			addrs = append(addrs, f[4])
		}
	}
	sort.Strings(addrs)
	return addrs
}

// nonLoopbackIPv4 is an address of the runner other than 127.0.0.0/8.
func nonLoopbackIPv4(t *testing.T) net.IP {
	t.Helper()
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		t.Fatal(err)
	}
	for _, a := range addrs {
		if n, ok := a.(*net.IPNet); ok && n.IP.To4() != nil && !n.IP.IsLoopback() {
			return n.IP
		}
	}
	t.Fatal("the runner has no non-loopback IPv4 address")
	return nil
}

// closeListener shuts the panel's 127.0.0.1:port listening socket down from outside
// (S1 listener loss): pidfd_getfd copies the socket into this process, and shutdown
// acts on the shared socket. The panel is a child of the test process, which the
// ptrace access check of pidfd_getfd allows.
func closeListener(t *testing.T, pid, port int) {
	t.Helper()
	inode := listenInode(t, port)
	fdDir := fmt.Sprintf("/proc/%d/fd", pid)
	entries, err := os.ReadDir(fdDir)
	if err != nil {
		t.Fatal(err)
	}
	target := -1
	for _, e := range entries {
		link, err := os.Readlink(filepath.Join(fdDir, e.Name()))
		if err == nil && link == "socket:["+inode+"]" {
			target, _ = strconv.Atoi(e.Name())
		}
	}
	if target < 0 {
		t.Fatalf("no fd of pid %d is socket inode %s", pid, inode)
	}
	pidfd, _, e := syscall.Syscall(434 /* pidfd_open */, uintptr(pid), 0, 0)
	if e != 0 {
		t.Fatalf("pidfd_open: %v", e)
	}
	defer syscall.Close(int(pidfd))
	fd, _, e := syscall.Syscall(438 /* pidfd_getfd */, pidfd, uintptr(target), 0)
	if e != 0 {
		t.Fatalf("pidfd_getfd: %v", e)
	}
	defer syscall.Close(int(fd))
	if err := syscall.Shutdown(int(fd), syscall.SHUT_RDWR); err != nil {
		t.Fatalf("shutdown of the panel listener: %v", err)
	}
	t.Logf("listener 127.0.0.1:%d (pid %d fd %d, inode %s) shut down from outside", port, pid, target, inode)
}

// listenInode finds the socket inode of the listening 127.0.0.1:port in /proc/net/tcp.
func listenInode(t *testing.T, port int) string {
	t.Helper()
	f, err := os.Open("/proc/net/tcp")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	want := fmt.Sprintf("0100007F:%04X", port)
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		fields := strings.Fields(sc.Text())
		if len(fields) > 9 && fields[1] == want && fields[3] == "0A" {
			return fields[9]
		}
	}
	t.Fatalf("no listening socket %s in /proc/net/tcp", want)
	return ""
}

// holderReport describes, from /proc, each process that ss names for the port: its
// executable, state, parent, wait channel, current system call and threads. It is
// printed when the port still accepts after the panel exited.
func holderReport(port int) string {
	out, _ := exec.Command("ss", "-ltnpH", "sport = :"+strconv.Itoa(port)).CombinedOutput()
	var b strings.Builder
	for _, m := range regexp.MustCompile(`pid=([0-9]+),fd=([0-9]+)`).FindAllStringSubmatch(string(out), -1) {
		dir := "/proc/" + m[1]
		exe, _ := os.Readlink(dir + "/exe")
		status, _ := os.ReadFile(dir + "/status")
		wchan, _ := os.ReadFile(dir + "/wchan")
		sysc, _ := os.ReadFile(dir + "/syscall")
		fmt.Fprintf(&b, "holder pid %s fd %s exe %s wchan %q syscall %q\n", m[1], m[2], exe, wchan, strings.TrimSpace(string(sysc)))
		for _, line := range strings.Split(string(status), "\n") {
			for _, k := range []string{"Name:", "State:", "PPid:", "Threads:", "SigBlk:", "SigPnd:", "ShdPnd:", "NSpid:"} {
				if strings.HasPrefix(line, k) {
					fmt.Fprintf(&b, "  %s\n", line)
				}
			}
		}
		tasks, _ := os.ReadDir(dir + "/task")
		for _, task := range tasks {
			stat, _ := os.ReadFile(dir + "/task/" + task.Name() + "/stat")
			tw, _ := os.ReadFile(dir + "/task/" + task.Name() + "/wchan")
			ts, _ := os.ReadFile(dir + "/task/" + task.Name() + "/syscall")
			fields := strings.Fields(string(stat))
			state := ""
			if len(fields) > 2 {
				state = fields[2]
			}
			fmt.Fprintf(&b, "  task %s state %s wchan %q syscall %q\n", task.Name(), state, tw, strings.TrimSpace(string(ts)))
		}
	}
	return b.String()
}

// cmdlines reads the argument vector of every process (S18).
func cmdlines() map[int][]string {
	out := map[int][]string{}
	entries, _ := os.ReadDir("/proc")
	for _, e := range entries {
		pid, err := strconv.Atoi(e.Name())
		if err != nil {
			continue
		}
		raw, err := os.ReadFile(filepath.Join("/proc", e.Name(), "cmdline"))
		if err != nil || len(raw) == 0 {
			continue
		}
		out[pid] = strings.Split(strings.TrimRight(string(raw), "\x00"), "\x00")
	}
	return out
}
