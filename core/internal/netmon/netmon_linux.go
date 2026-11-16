package netmon

import (
	"bufio"
	"os"
	"sort"
	"strconv"
	"strings"
)

// watch polls: reading two small files of /proc every few seconds is cheap,
// and proper netlink notifications come with the Linux stage.
func watch(kick func()) (func(), error) {
	return poll(kick, pollEvery), nil
}

const (
	rtfUp     = 0x1
	rtfReject = 0x200
)

func read(ignore []string) (State, error) {
	skip := func(iface string) bool {
		if iface == "lo" {
			return true
		}
		for _, name := range ignore {
			if strings.EqualFold(iface, name) {
				return true
			}
		}
		return false
	}
	var st State
	var via []string

	// Iface Destination Gateway Flags RefCnt Use Metric Mask MTU Window IRTT
	f, err := os.Open("/proc/net/route")
	if err != nil {
		return State{}, err
	}
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		p := strings.Fields(sc.Text())
		if len(p) < 8 || p[1] != "00000000" || p[7] != "00000000" || skip(p[0]) {
			continue
		}
		if flags, err := strconv.ParseUint(p[3], 16, 32); err != nil || flags&rtfUp == 0 {
			continue
		}
		st.V4 = true
		via = append(via, "4:"+p[0]+":"+p[2])
	}
	f.Close()

	// dest plen src plen nexthop metric refcnt use flags iface; no file at
	// all means IPv6 is switched off, which is not an error.
	if f, err := os.Open("/proc/net/ipv6_route"); err == nil {
		sc := bufio.NewScanner(f)
		for sc.Scan() {
			p := strings.Fields(sc.Text())
			if len(p) < 10 || p[1] != "00" || strings.Trim(p[0], "0") != "" || skip(p[9]) {
				continue
			}
			if flags, err := strconv.ParseUint(p[8], 16, 32); err != nil || flags&rtfUp == 0 || flags&rtfReject != 0 {
				continue
			}
			st.V6 = true
			via = append(via, "6:"+p[9]+":"+p[4])
		}
		f.Close()
	}
	sort.Strings(via)
	st.via = strings.Join(via, ",")
	return st, nil
}
