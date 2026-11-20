//go:build darwin

package conformance

// The selectors and the options of waitid, and the size of the buffer that
// the kernel fills. The idtype_t of macOS is P_ALL = 0, P_PID = 1, P_PGID = 2.
// The siginfo_t of macOS is 104 bytes, so the buffer has space to spare.
const (
	waitidPid    = 1
	waitidExited = 0x04
	waitidNowait = 0x20
	siginfoBytes = 128
)
