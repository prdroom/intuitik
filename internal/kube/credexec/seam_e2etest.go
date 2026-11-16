//go:build e2etest

package credexec

import (
	"os"
	"strconv"
	"syscall"
)

// seams reads the helper's test inputs (packet M1-T3b E7, E11). This file is compiled
// only with the e2etest tag.
//
//	INTUITIK_TEST_CREDEXEC_JOB_ERROR=<code>      the job assignment fails with <code> (TestRun_JobRefused)
//	INTUITIK_TEST_CREDEXEC_EXIT_AFTER_START=1    the helper exits after starting the program, before its
//	                                             result frame (TestRun_HelperDiesAfterStart)
func seams() helperSeams {
	var s helperSeams
	if v := os.Getenv("INTUITIK_TEST_CREDEXEC_JOB_ERROR"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			s.jobError = syscall.Errno(n)
		}
	}
	s.exitAfterStart = os.Getenv("INTUITIK_TEST_CREDEXEC_EXIT_AFTER_START") == "1"
	return s
}
