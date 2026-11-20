//go:build linux

package conformance

// The selectors and the options of waitid, and the size of the buffer that
// the kernel fills. The siginfo_t of Linux is 128 bytes.
const (
	waitidPid    = 1
	waitidExited = 4
	waitidNowait = 0x01000000
	siginfoBytes = 128
)
