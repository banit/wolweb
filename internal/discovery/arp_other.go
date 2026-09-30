//go:build !linux

package discovery

import (
	"context"
	"errors"
	"net/netip"
)

func arpScan(context.Context, LocalNet, []netip.Addr, func(int)) ([]Hit, error) {
	return nil, errors.New("ARP-Scan gibt es nur unter Linux")
}
