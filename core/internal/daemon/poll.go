package daemon

import (
	"context"
	"net"
	"path/filepath"
	"strings"
	"time"

	"strelka/internal/clash"
	"strelka/internal/confgen"
	"strelka/internal/health"
	"strelka/proto"
)

// pollEngine asks the running engine for its connections once a second and
// turns the answer into four things: the "connections" topic, the "traffic"
// topic, every other second the traffic and connection counts in State, and
// - what health cares about - whether bytes are coming back through the
// VPN. It runs for the daemon's lifetime and idles while the VPN is off.
func (d *Daemon) pollEngine() {
	tick := time.NewTicker(time.Second)
	defer tick.Stop()
	var last clash.Snapshot
	var lastAPI *clash.Client
	var rx map[string]int64 // bytes received per VPN connection at the last look
	grew := false
	n := 0
	for {
		select {
		case <-d.done:
			return
		case <-tick.C:
		}
		api := d.api.Load()
		if api == nil || !d.live.Load() {
			lastAPI, rx, grew = nil, nil, false
			continue
		}
		ctx, cancel := context.WithTimeout(context.Background(), 900*time.Millisecond)
		snap, err := api.Snapshot(ctx)
		cancel()
		if err != nil {
			continue // a restart is under way; the supervisor will say so
		}

		var up, down int64
		// The counters start over with every engine process.
		if api == lastAPI {
			up, down = max(snap.UpTotal-last.UpTotal, 0), max(snap.DownTotal-last.DownTotal, 0)
		} else {
			rx = nil
		}
		last, lastAPI = snap, api

		var more bool
		rx, more = received(rx, snap)
		grew = grew || more
		n++
		if n%2 == 0 {
			// The passive signal, every two seconds: a server that is
			// delivering somebody's download needs no probe to prove it.
			if grew {
				d.post(func() { d.feed(health.Traffic{}) })
			}
			grew = false
		}

		// Nobody is watching: the rest is for clients only.
		if d.srv.Clients() == 0 {
			continue
		}
		d.srv.Publish(proto.TopicTraffic, proto.TrafficSample{T: time.Now().UTC().Truncate(time.Second), Up: up, Down: down})
		if d.srv.Subscribers(proto.TopicConnections) > 0 {
			d.srv.Publish(proto.TopicConnections, connections(snap))
		}
		if n%2 == 0 {
			traffic := proto.Traffic{Up: up, Down: down, UpTotal: snap.UpTotal, DownTotal: snap.DownTotal}
			conns := proto.Conns{Total: len(snap.Conns)}
			for _, c := range snap.Conns {
				if !c.Direct() {
					conns.VPN++
				}
			}
			d.post(func() {
				if d.st.Traffic == traffic && d.st.Conns == conns {
					return
				}
				d.st.Traffic, d.st.Conns = traffic, conns
				d.touch()
			})
		}
	}
}

// received compares the bytes that came back over each VPN connection with
// the previous look. It answers per connection, not from the totals: the
// engine's total counts direct traffic too, and that proves nothing about
// the server. Nor does a speed test's download, which goes through whatever
// server is being measured. prev is nil on the first look at an engine,
// when there is nothing to compare with yet.
func received(prev map[string]int64, snap clash.Snapshot) (now map[string]int64, grew bool) {
	now = make(map[string]int64, len(snap.Conns))
	for _, c := range snap.Conns {
		if c.Direct() || strings.HasSuffix(c.Inbound, "/"+confgen.GateTag) {
			continue
		}
		now[c.ID] = c.Download
		if prev == nil {
			continue
		}
		// A connection that is new and already has an answer counts as well.
		if c.Download > prev[c.ID] {
			grew = true
		}
	}
	return now, grew
}

func connections(snap clash.Snapshot) []proto.Connection {
	out := make([]proto.Connection, 0, len(snap.Conns))
	for _, c := range snap.Conns {
		pc := proto.Connection{
			ID: c.ID, Host: c.Host, Route: proto.ListVPN, Chain: c.Chains,
			Up: c.Upload, Down: c.Download, Started: c.Start,
		}
		if c.Direct() {
			pc.Route = proto.ListDirect
		}
		if pc.Chain == nil {
			pc.Chain = []string{}
		}
		if c.ProcessPath != "" {
			pc.Process = filepath.Base(c.ProcessPath)
		}
		if c.DestIP != "" {
			pc.Dest = net.JoinHostPort(c.DestIP, c.DestPort)
		}
		if pc.Host == "" {
			pc.Host = c.DestIP
		}
		out = append(out, pc)
	}
	return out
}
