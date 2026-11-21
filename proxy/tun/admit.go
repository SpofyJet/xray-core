package tun

import (
	"sync/atomic"

	"github.com/xtls/xray-core/common/net"
)

type Admit func(network net.Network, source, destination net.Destination) (ok bool, outbound string)

var admit atomic.Pointer[Admit]

func SetAdmit(f Admit) {
	if f == nil {
		admit.Store(nil)
		return
	}
	admit.Store(&f)
}

func admitted(source, destination net.Destination) (bool, string) {
	f := admit.Load()
	if f == nil {
		return true, ""
	}
	return (*f)(destination.Network, source, destination)
}
