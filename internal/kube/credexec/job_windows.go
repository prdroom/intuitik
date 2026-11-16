//go:build windows

package credexec

import (
	"syscall"
	"unsafe"
)

// The job object calls of the helper (D3). unsafe is used only here, for the address and
// size of the limit structure that SetInformationJobObject reads (E15).

var (
	kernel32                     = syscall.NewLazyDLL("kernel32.dll")
	procCreateJobObjectW         = kernel32.NewProc("CreateJobObjectW")
	procSetInformationJobObject  = kernel32.NewProc("SetInformationJobObject")
	procAssignProcessToJobObject = kernel32.NewProc("AssignProcessToJobObject")
)

const (
	jobObjectExtendedLimitInformation = 9      // JOBOBJECTINFOCLASS
	jobObjectLimitKillOnJobClose      = 0x2000 // JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE
)

// JOBOBJECT_BASIC_LIMIT_INFORMATION
type jobBasicLimits struct {
	PerProcessUserTimeLimit int64
	PerJobUserTimeLimit     int64
	LimitFlags              uint32
	MinimumWorkingSetSize   uintptr
	MaximumWorkingSetSize   uintptr
	ActiveProcessLimit      uint32
	Affinity                uintptr
	PriorityClass           uint32
	SchedulingClass         uint32
}

// IO_COUNTERS
type jobIOCounters struct {
	ReadOperationCount  uint64
	WriteOperationCount uint64
	OtherOperationCount uint64
	ReadTransferCount   uint64
	WriteTransferCount  uint64
	OtherTransferCount  uint64
}

// JOBOBJECT_EXTENDED_LIMIT_INFORMATION
type jobExtendedLimits struct {
	Basic                 jobBasicLimits
	IO                    jobIOCounters
	ProcessMemoryLimit    uintptr
	JobMemoryLimit        uintptr
	PeakProcessMemoryUsed uintptr
	PeakJobMemoryUsed     uintptr
}

// joinKillOnCloseJob creates an unnamed job whose last closed handle ends every process
// in it, and assigns the current process to it (nested jobs). The handle is created
// without a security descriptor that allows inheritance, so a child never inherits it;
// it stays open until the process exits. It returns the error code of the failing call.
func joinKillOnCloseJob() (syscall.Errno, bool) {
	h, _, e := procCreateJobObjectW.Call(0, 0)
	if h == 0 {
		return errnoOf(e), false
	}
	info := jobExtendedLimits{}
	info.Basic.LimitFlags = jobObjectLimitKillOnJobClose
	r, _, e := procSetInformationJobObject.Call(h, jobObjectExtendedLimitInformation, uintptr(unsafe.Pointer(&info)), unsafe.Sizeof(info))
	if r == 0 {
		syscall.CloseHandle(syscall.Handle(h))
		return errnoOf(e), false
	}
	self, err := syscall.GetCurrentProcess()
	if err != nil {
		syscall.CloseHandle(syscall.Handle(h))
		return errnoOf(err), false
	}
	r, _, e = procAssignProcessToJobObject.Call(h, uintptr(self))
	if r == 0 {
		syscall.CloseHandle(syscall.Handle(h))
		return errnoOf(e), false
	}
	return 0, true
}

func errnoOf(err error) syscall.Errno {
	if errno, ok := err.(syscall.Errno); ok && errno != 0 {
		return errno
	}
	return syscall.Errno(1)
}
