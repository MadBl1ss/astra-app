package daemon

import (
	"context"
	"net"
	"path/filepath"
	"time"

	"strelka/internal/clash"
	"strelka/proto"
)

// pollEngine asks the running engine for its connections once a second and
// turns the answer into three things: the "connections" topic, the "traffic"
// topic and, every other second, the traffic and connection counts in State.
// It runs for the daemon's lifetime and idles while the VPN is off or nobody
// is connected.
func (d *Daemon) pollEngine() {
	tick := time.NewTicker(time.Second)
	defer tick.Stop()
	var last clash.Snapshot
	var lastAPI *clash.Client
	n := 0
	for {
		select {
		case <-d.done:
			return
		case <-tick.C:
		}
		api := d.api.Load()
		if api == nil || !d.live.Load() || d.srv.Clients() == 0 {
			lastAPI = nil
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
		}
		last, lastAPI = snap, api
		d.srv.Publish(proto.TopicTraffic, proto.TrafficSample{T: time.Now().UTC().Truncate(time.Second), Up: up, Down: down})

		if d.srv.Subscribers(proto.TopicConnections) > 0 {
			d.srv.Publish(proto.TopicConnections, connections(snap))
		}

		if n++; n%2 == 0 {
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
