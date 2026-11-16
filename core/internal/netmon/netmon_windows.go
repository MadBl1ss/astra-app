package netmon

import (
	"fmt"
	"net"
	"sort"
	"strings"
	"sync"
	"unsafe"

	"golang.org/x/sys/windows"
)

// The system calls back on its own thread whenever an adapter or a route
// changes. A callback made with NewCallback can never be freed, so there is
// exactly one for the whole process and it fans out to every monitor.
var (
	callbackOnce sync.Once
	callback     uintptr

	watchersMu sync.Mutex
	watchers   = map[int]func(){}
	watcherSeq int
)

// changed has the shape iphlpapi wants for both notifications:
// (CallerContext, Row, NotificationType). The row says too little to be of
// use - the docs call it "incomplete data" - so the routes are simply read
// again once the burst is over.
func changed(_, _ uintptr, _ uint32) uintptr {
	watchersMu.Lock()
	for _, kick := range watchers {
		kick() // never blocks
	}
	watchersMu.Unlock()
	return 0
}

func watch(kick func()) (func(), error) {
	callbackOnce.Do(func() { callback = windows.NewCallback(changed) })

	watchersMu.Lock()
	watcherSeq++
	id := watcherSeq
	watchers[id] = kick
	watchersMu.Unlock()
	forget := func() {
		watchersMu.Lock()
		delete(watchers, id)
		watchersMu.Unlock()
	}

	var ifaces, routes windows.Handle
	if err := windows.NotifyIpInterfaceChange(windows.AF_UNSPEC, callback, nil, false, &ifaces); err != nil {
		forget()
		return nil, fmt.Errorf("NotifyIpInterfaceChange: %w", err)
	}
	if err := windows.NotifyRouteChange2(windows.AF_UNSPEC, callback, nil, false, &routes); err != nil {
		windows.CancelMibChangeNotify2(ifaces)
		forget()
		return nil, fmt.Errorf("NotifyRouteChange2: %w", err)
	}
	return func() {
		// Cancelling waits for a callback in flight, and the callback takes
		// watchersMu: the lock must be free by the time we cancel, or the two
		// wait for each other. This is the deadlock the documentation of
		// CancelMibChangeNotify2 warns about.
		forget()
		windows.CancelMibChangeNotify2(ifaces)
		windows.CancelMibChangeNotify2(routes)
	}, nil
}

func read(ignore []string) (State, error) {
	// A route through an adapter that is down, or through our own tunnel,
	// leads nowhere.
	usable := map[uint32]bool{}
	ifaces, err := net.Interfaces()
	if err != nil {
		return State{}, err
	}
	for _, ifi := range ifaces {
		up := ifi.Flags&net.FlagUp != 0 && ifi.Flags&net.FlagLoopback == 0
		for _, name := range ignore {
			if strings.EqualFold(ifi.Name, name) {
				up = false
			}
		}
		usable[uint32(ifi.Index)] = up
	}

	var table *windows.MibIpForwardTable2
	if err := windows.GetIpForwardTable2(windows.AF_UNSPEC, &table); err != nil {
		return State{}, fmt.Errorf("GetIpForwardTable2: %w", err)
	}
	defer windows.FreeMibTable(unsafe.Pointer(table))

	var st State
	var via []string
	for _, r := range table.Rows() {
		if r.DestinationPrefix.PrefixLength != 0 || r.Loopback != 0 || !usable[r.InterfaceIndex] {
			continue
		}
		switch r.DestinationPrefix.Prefix.Family {
		case windows.AF_INET:
			st.V4 = true
		case windows.AF_INET6:
			st.V6 = true
		default:
			continue
		}
		via = append(via, fmt.Sprintf("%d:%d:%x", r.DestinationPrefix.Prefix.Family, r.InterfaceIndex, r.NextHop.Data))
	}
	sort.Strings(via)
	st.via = strings.Join(via, ",")
	return st, nil
}
