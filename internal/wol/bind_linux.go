//go:build linux

package wol

import "syscall"

// bindToDevice legt den Socket auf die Schnittstelle fest. Klappt das nicht (Kernel vor 5.7 ohne
// CAP_NET_RAW), bleibt es bei der Absender-IP – so wie bisher.
func bindToDevice(iface string) func(network, address string, c syscall.RawConn) error {
	return func(_, _ string, c syscall.RawConn) error {
		return c.Control(func(fd uintptr) {
			_ = syscall.SetsockoptString(int(fd), syscall.SOL_SOCKET, syscall.SO_BINDTODEVICE, iface)
		})
	}
}
