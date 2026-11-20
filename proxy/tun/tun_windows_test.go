package tun

import (
	"fmt"
	"net/netip"
	"os"
	"slices"
	"testing"
	"time"

	"golang.org/x/sys/windows"
	"golang.zx2c4.com/wintun"
	"golang.zx2c4.com/wireguard/windows/tunnel/winipcfg"
)

func forwardRow(t *testing.T, luid winipcfg.LUID, protocol winipcfg.RouteProtocol, destination, nextHop string, metric uint32) winipcfg.MibIPforwardRow2 {
	t.Helper()
	row := winipcfg.MibIPforwardRow2{InterfaceLUID: luid, Protocol: protocol, Metric: metric}
	if err := row.DestinationPrefix.SetPrefix(netip.MustParsePrefix(destination)); err != nil {
		t.Fatal(err)
	}
	if err := row.NextHop.SetAddr(netip.MustParseAddr(nextHop)); err != nil {
		t.Fatal(err)
	}
	return row
}

func addressRow(t *testing.T, luid winipcfg.LUID, address string, bits uint8, origin winipcfg.PrefixOrigin) winipcfg.MibUnicastIPAddressRow {
	t.Helper()
	row := winipcfg.MibUnicastIPAddressRow{InterfaceLUID: luid, OnLinkPrefixLength: bits, PrefixOrigin: origin}
	if err := row.Address.SetAddr(netip.MustParseAddr(address)); err != nil {
		t.Fatal(err)
	}
	return row
}

func routeName(row *winipcfg.MibIPforwardRow2) string {
	return fmt.Sprintf("%s via %s metric %d", row.DestinationPrefix.Prefix(), row.NextHop.Addr().WithZone(""), row.Metric)
}

func routeNames(rows []*winipcfg.MibIPforwardRow2) []string {
	names := make([]string, 0, len(rows))
	for _, row := range rows {
		names = append(names, routeName(row))
	}
	slices.Sort(names)
	return names
}

func missingNames(missing map[route]uint32) []string {
	names := make([]string, 0, len(missing))
	for r, metric := range missing {
		names = append(names, fmt.Sprintf("%s via %s metric %d", r.destination, r.nextHop, metric))
	}
	slices.Sort(names)
	return names
}

func addressNames(rows []*winipcfg.MibUnicastIPAddressRow) []string {
	names := make([]string, 0, len(rows))
	for _, row := range rows {
		names = append(names, netip.PrefixFrom(row.Address.Addr(), int(row.OnLinkPrefixLength)).String())
	}
	slices.Sort(names)
	return names
}

func prefixNames(prefixes []netip.Prefix) []string {
	names := make([]string, 0, len(prefixes))
	for _, p := range prefixes {
		names = append(names, p.String())
	}
	slices.Sort(names)
	return names
}

func same(t *testing.T, what string, got, want []string) {
	t.Helper()
	if !slices.Equal(got, want) {
		t.Errorf("%s: %q, want %q", what, got, want)
	}
}

func TestRouteChangesKeepWhatIsWantedAndTouchOnlyStaticRoutesOfTheInterface(t *testing.T) {
	const ours, theirs = winipcfg.LUID(1), winipcfg.LUID(2)
	table := []winipcfg.MibIPforwardRow2{
		forwardRow(t, ours, winipcfg.RouteProtocolNetMgmt, "0.0.0.0/1", "0.0.0.0", 0),
		forwardRow(t, ours, winipcfg.RouteProtocolNetMgmt, "128.0.0.0/1", "0.0.0.0", 5),
		forwardRow(t, ours, winipcfg.RouteProtocolNetMgmt, "10.0.0.0/8", "0.0.0.0", 0),
		forwardRow(t, ours, winipcfg.RouteProtocolLocal, "172.19.0.0/30", "0.0.0.0", 256),
		forwardRow(t, theirs, winipcfg.RouteProtocolNetMgmt, "0.0.0.0/0", "192.0.2.1", 0),
	}
	routes := []*winipcfg.RouteData{
		{Destination: netip.MustParsePrefix("0.0.0.0/1"), NextHop: netip.IPv4Unspecified()},
		{Destination: netip.MustParsePrefix("128.0.0.0/1"), NextHop: netip.IPv4Unspecified()},
		{Destination: netip.MustParsePrefix("198.18.7.1/15"), NextHop: netip.IPv4Unspecified()},
		{Destination: netip.MustParsePrefix("::/0"), NextHop: netip.IPv6Unspecified()},
	}
	stale, missing := routeChanges(table, ours, windows.AF_INET, routes)
	same(t, "removed", routeNames(stale), []string{"10.0.0.0/8 via 0.0.0.0 metric 0", "128.0.0.0/1 via 0.0.0.0 metric 5"})
	same(t, "added", missingNames(missing), []string{"128.0.0.0/1 via 0.0.0.0 metric 0", "198.18.0.0/15 via 0.0.0.0 metric 0"})
}

func TestRouteChangesTakeAnIPv6NextHopWithItsZoneForTheWantedOne(t *testing.T) {
	const ours = winipcfg.LUID(1)
	table := []winipcfg.MibIPforwardRow2{forwardRow(t, ours, winipcfg.RouteProtocolNetMgmt, "::/0", "::%7", 0)}
	routes := []*winipcfg.RouteData{{Destination: netip.MustParsePrefix("::/0"), NextHop: netip.IPv6Unspecified()}}
	stale, missing := routeChanges(table, ours, windows.AF_INET6, routes)
	same(t, "removed", routeNames(stale), []string{})
	same(t, "added", missingNames(missing), []string{})
}

func TestAddressChangesKeepWhatIsWantedAndTouchOnlyTheInterface(t *testing.T) {
	const ours, theirs = winipcfg.LUID(1), winipcfg.LUID(2)
	rows4 := []winipcfg.MibUnicastIPAddressRow{
		addressRow(t, ours, "172.19.0.1", 30, winipcfg.PrefixOriginManual),
		addressRow(t, ours, "172.19.0.5", 30, winipcfg.PrefixOriginManual),
		addressRow(t, ours, "169.254.3.4", 16, winipcfg.PrefixOriginWellKnown),
		addressRow(t, theirs, "192.0.2.10", 24, winipcfg.PrefixOriginManual),
	}
	wanted := []netip.Prefix{netip.MustParsePrefix("172.19.0.1/30"), netip.MustParsePrefix("172.19.0.9/30"), netip.MustParsePrefix("fdfe:dcba:9876::1/126")}
	stale, missing := addressChanges(rows4, ours, windows.AF_INET, wanted)
	same(t, "removed", addressNames(stale), []string{"169.254.3.4/16", "172.19.0.5/30"})
	same(t, "added", prefixNames(missing), []string{"172.19.0.9/30"})

	rows6 := []winipcfg.MibUnicastIPAddressRow{
		addressRow(t, ours, "fdfe:dcba:9876::1%7", 126, winipcfg.PrefixOriginManual),
		addressRow(t, ours, "fe80::1%7", 64, winipcfg.PrefixOriginWellKnown),
	}
	stale, missing = addressChanges(rows6, ours, windows.AF_INET6, wanted)
	same(t, "removed", addressNames(stale), []string{"fe80::1/64"})
	same(t, "added", prefixNames(missing), []string{})
}

func forwardTable(t *testing.T, family winipcfg.AddressFamily) []winipcfg.MibIPforwardRow2 {
	t.Helper()
	table, err := winipcfg.GetIPForwardTable2(family)
	if err != nil {
		t.Fatal(err)
	}
	return table
}

func routesOn(table []winipcfg.MibIPforwardRow2, keep func(*winipcfg.MibIPforwardRow2) bool) []string {
	var names []string
	for i := range table {
		if row := &table[i]; keep(row) {
			names = append(names, fmt.Sprintf("%d %s", row.InterfaceLUID, routeName(row)))
		}
	}
	slices.Sort(names)
	return names
}

func lost(before, after []string) []string {
	var gone []string
	for _, name := range before {
		if _, found := slices.BinarySearch(after, name); !found {
			gone = append(gone, name)
		}
	}
	return gone
}

func settled(t *testing.T, family winipcfg.AddressFamily, keep func(*winipcfg.MibIPforwardRow2) bool) []string {
	t.Helper()
	last := routesOn(forwardTable(t, family), keep)
	for end := time.Now().Add(3 * time.Second); time.Now().Before(end); {
		time.Sleep(200 * time.Millisecond)
		now := routesOn(forwardTable(t, family), keep)
		if slices.Equal(now, last) {
			return now
		}
		last = now
	}
	t.Fatalf("the routes Windows makes for the interface keep changing: %q", last)
	return nil
}

func ready(t *testing.T, luid winipcfg.LUID) {
	t.Helper()
	for end := time.Now().Add(10 * time.Second); ; time.Sleep(50 * time.Millisecond) {
		_, err4 := luid.IPInterface(windows.AF_INET)
		_, err6 := luid.IPInterface(windows.AF_INET6)
		if err4 == nil && err6 == nil {
			return
		}
		if time.Now().After(end) {
			t.Fatalf("the interface is not there for IP: %v, %v", err4, err6)
		}
	}
}

func TestTheInterfaceComesToTheWantedRoutesAndAddressesAndLeavesTheRestAlone(t *testing.T) {
	if os.Getenv("SPOFY_ADAPTER_TESTS") == "" {
		t.Skip("needs wintun.dll beside the test and an elevated process: SPOFY_ADAPTER_TESTS=1")
	}
	const name = "spofyroutes0"
	adapter, err := wintun.CreateAdapter(name, "Spofy", AdapterGUID(name))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { adapter.Close() })
	session, err := adapter.StartSession(0x20000)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(session.End)
	luid := winipcfg.LUID(adapter.LUID())
	ready(t, luid)

	elsewhere := func(row *winipcfg.MibIPforwardRow2) bool {
		return row.InterfaceLUID != luid && row.Protocol == winipcfg.RouteProtocolNetMgmt
	}
	others4 := routesOn(forwardTable(t, windows.AF_INET), elsewhere)
	others6 := routesOn(forwardTable(t, windows.AF_INET6), elsewhere)
	system := func(row *winipcfg.MibIPforwardRow2) bool {
		return row.InterfaceLUID == luid && row.Protocol != winipcfg.RouteProtocolNetMgmt
	}
	forAddress := map[winipcfg.AddressFamily][]string{}

	addresses := func(family winipcfg.AddressFamily, list ...string) {
		t.Helper()
		var wanted []netip.Prefix
		for _, a := range list {
			wanted = append(wanted, netip.MustParsePrefix(a))
		}
		if err := setAddresses(luid, family, wanted); err != nil {
			t.Fatalf("addresses %v: %v", list, err)
		}
		rows, err := winipcfg.GetUnicastIPAddressTable(family)
		if err != nil {
			t.Fatal(err)
		}
		var manual []*winipcfg.MibUnicastIPAddressRow
		for i := range rows {
			if rows[i].InterfaceLUID == luid && rows[i].PrefixOrigin == winipcfg.PrefixOriginManual {
				manual = append(manual, &rows[i])
			}
		}
		same(t, fmt.Sprintf("addresses after %v", list), addressNames(manual), prefixNames(wanted))
		_, missing := addressChanges(rows, luid, family, wanted)
		same(t, fmt.Sprintf("addresses still to add after %v", list), prefixNames(missing), []string{})
	}
	routes := func(family winipcfg.AddressFamily, list map[string]uint32) {
		t.Helper()
		hop := netip.IPv4Unspecified()
		if family == windows.AF_INET6 {
			hop = netip.IPv6Unspecified()
		}
		var wanted []*winipcfg.RouteData
		var want []string
		for destination, metric := range list {
			wanted = append(wanted, &winipcfg.RouteData{Destination: netip.MustParsePrefix(destination), NextHop: hop, Metric: metric})
			want = append(want, fmt.Sprintf("%d %s via %s metric %d", luid, destination, hop, metric))
		}
		slices.Sort(want)
		if err := setRoutes(luid, family, wanted); err != nil {
			t.Fatalf("routes %v: %v", list, err)
		}
		table := forwardTable(t, family)
		same(t, fmt.Sprintf("static routes after %v", list), routesOn(table, func(row *winipcfg.MibIPforwardRow2) bool {
			return row.InterfaceLUID == luid && row.Protocol == winipcfg.RouteProtocolNetMgmt
		}), want)
		stale, missing := routeChanges(table, luid, family, wanted)
		same(t, fmt.Sprintf("routes still to remove after %v", list), routeNames(stale), []string{})
		same(t, fmt.Sprintf("routes still to add after %v", list), missingNames(missing), []string{})
		if gone := lost(forAddress[family], routesOn(table, system)); len(gone) > 0 {
			t.Errorf("routes %v took routes Windows made for the address of the interface: %q", list, gone)
		}
	}

	addresses(windows.AF_INET, "198.18.0.1/30")
	addresses(windows.AF_INET6, "2001:db8:5::1/126")
	forAddress[windows.AF_INET] = settled(t, windows.AF_INET, system)
	forAddress[windows.AF_INET6] = settled(t, windows.AF_INET6, system)
	routes(windows.AF_INET, map[string]uint32{"198.51.100.0/24": 0, "203.0.113.0/24": 0})
	routes(windows.AF_INET, map[string]uint32{"203.0.113.0/24": 0, "192.0.2.0/24": 0})
	routes(windows.AF_INET, map[string]uint32{"203.0.113.0/24": 7, "192.0.2.0/24": 0})
	routes(windows.AF_INET, map[string]uint32{"203.0.113.0/24": 7, "192.0.2.0/24": 0})
	routes(windows.AF_INET6, map[string]uint32{"2001:db8:1::/48": 0})
	routes(windows.AF_INET6, map[string]uint32{"2001:db8:1::/48": 0, "2001:db8:2::/48": 3})
	routes(windows.AF_INET, nil)
	routes(windows.AF_INET6, nil)
	addresses(windows.AF_INET, "198.18.0.5/30")
	addresses(windows.AF_INET6, "2001:db8:5::5/126", "2001:db8:6::1/64")
	addresses(windows.AF_INET6, "2001:db8:6::1/64")
	addresses(windows.AF_INET)

	if gone := lost(others4, routesOn(forwardTable(t, windows.AF_INET), elsewhere)); len(gone) > 0 {
		t.Errorf("static routes of other interfaces are gone: %q", gone)
	}
	if gone := lost(others6, routesOn(forwardTable(t, windows.AF_INET6), elsewhere)); len(gone) > 0 {
		t.Errorf("static routes of other interfaces are gone: %q", gone)
	}
}
