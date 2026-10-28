//go:build !linux

package wol

import "syscall"

// bindToDevice: außerhalb von Linux genügt die Absender-IP.
func bindToDevice(string) func(network, address string, c syscall.RawConn) error { return nil }
