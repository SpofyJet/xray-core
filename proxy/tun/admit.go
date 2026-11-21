package tun

import (
	"sync/atomic"

	"github.com/xtls/xray-core/common/net"
)

type Admit func(network net.Network, source, destination net.Destination) bool

var admit atomic.Pointer[Admit]

func SetAdmit(f Admit) {
	if f == nil {
		admit.Store(nil)
		return
	}
	admit.Store(&f)
}

func admitted(source, destination net.Destination) bool {
	f := admit.Load()
	return f == nil || (*f)(destination.Network, source, destination)
}
