package main

import (
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"reflect"
	"strings"
	"sync"
	"time"

	"github.com/miekg/dns"
)

func (a *App) dnsLoop(ctx context.Context) {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	var stop func()
	var previous DNSSettings
	first := true
	defer func() {
		if stop != nil {
			stop()
		}
		a.flushDNS()
	}()
	for {
		s := a.dns.dataSettings()
		if first || !reflect.DeepEqual(previous, s) {
			first = false
			previous = s
			if stop != nil {
				stop()
				stop = nil
			}
			var err error
			if a.cfg.Mode == "vps" && s.Enabled && (s.ClassicEnabled || s.SmartEnabled) {
				stop, err = a.startDNSListeners(ctx, s)
			}
			a.dns.mu.Lock()
			a.dns.active = DNSSettings{}
			if err != nil {
				first = true
				a.dns.status = "پورت مستقیم باز نشد: " + err.Error() + "؛ اشغال پورت و مجوز NET_BIND_SERVICE را بررسی کن"
			} else if !s.Enabled {
				a.dns.status = "DNS خاموش است"
			} else {
				a.dns.active = s
				a.dns.status = "DoH فعال است"
				if s.ClassicEnabled {
					a.dns.status += " · TCP/UDP DNS فعال"
				}
				if s.SmartEnabled {
					a.dns.status += " · رلهٔ TLS فعال"
				}
			}
			a.dns.mu.Unlock()
		}
		select {
		case <-ctx.Done():
			return
		case <-a.dns.wake:
		case <-ticker.C:
			a.flushDNS()
		}
	}
}
func (a *App) startDNSListeners(parent context.Context, s DNSSettings) (func(), error) {
	ctx, cancel := context.WithCancel(parent)
	var udp net.PacketConn
	var tcp, relay net.Listener
	var err error
	cleanup := func() {
		cancel()
		if udp != nil {
			_ = udp.Close()
		}
		if tcp != nil {
			_ = tcp.Close()
		}
		if relay != nil {
			_ = relay.Close()
		}
	}
	if s.ClassicEnabled {
		udp, err = net.ListenPacket("udp", s.DNSListen)
		if err != nil {
			cleanup()
			return nil, fmt.Errorf("UDP DNS: %w", err)
		}
		tcp, err = net.Listen("tcp", s.DNSListen)
		if err != nil {
			cleanup()
			return nil, fmt.Errorf("TCP DNS: %w", err)
		}
	}
	if s.SmartEnabled {
		relay, err = net.Listen("tcp", s.TLSListen)
		if err != nil {
			cleanup()
			return nil, fmt.Errorf("TLS relay: %w", err)
		}
	}
	var servers []*dns.Server
	if udp != nil {
		handler := dns.HandlerFunc(func(w dns.ResponseWriter, q *dns.Msg) { a.serveClassicDNS(ctx, w, q) })
		servers = []*dns.Server{{PacketConn: udp, Handler: handler, UDPSize: 1232, ReadTimeout: 4 * time.Second, WriteTimeout: 4 * time.Second}, {Listener: tcp, Handler: handler, ReadTimeout: 4 * time.Second, WriteTimeout: 4 * time.Second, MaxTCPQueries: 32}}
		for _, srv := range servers {
			go func(srv *dns.Server) { _ = srv.ActivateAndServe() }(srv)
		}
	}
	if relay != nil {
		go a.acceptSmartTLS(ctx, relay, s)
	}
	var once sync.Once
	return func() {
		once.Do(func() {
			cleanup()
			for _, srv := range servers {
				c, cancel := context.WithTimeout(context.Background(), time.Second)
				_ = srv.ShutdownContext(c)
				cancel()
			}
		})
	}, nil
}
func (a *App) serveClassicDNS(ctx context.Context, w dns.ResponseWriter, q *dns.Msg) {
	refuse := func() { m := new(dns.Msg); m.SetRcode(q, dns.RcodeRefused); _ = w.WriteMsg(m) }
	if !a.dns.dataSettings().ClassicEnabled {
		refuse()
		return
	}
	host, _, e := net.SplitHostPort(w.RemoteAddr().String())
	if e != nil {
		refuse()
		return
	}
	wire, e := q.Pack()
	if e != nil {
		refuse()
		return
	}
	clean, e := sanitizeDNSQuery(wire)
	if e != nil {
		refuse()
		return
	}
	c, s, status := a.reserveDNS("", net.ParseIP(host))
	if status != 200 {
		refuse()
		return
	}
	select {
	case a.dns.busy <- struct{}{}:
		defer func() { <-a.dns.busy }()
	default:
		refuse()
		return
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	answer, e := a.answerDNS(ctx, clean, c, s)
	if e != nil {
		answer = new(dns.Msg)
		answer.SetRcode(q, dns.RcodeServerFailure)
	}
	if _, udp := w.RemoteAddr().(*net.UDPAddr); udp {
		size := 512
		if opt := q.IsEdns0(); opt != nil {
			size = min(1232, max(512, int(opt.UDPSize())))
		}
		answer.Truncate(size)
	}
	_ = w.WriteMsg(answer)
}

func (a *App) acceptSmartTLS(ctx context.Context, listener net.Listener, s DNSSettings) {
	slots := make(chan struct{}, 128)
	for {
		c, e := listener.Accept()
		if e != nil {
			return
		}
		select {
		case slots <- struct{}{}:
			go func() { defer func() { <-slots }(); a.handleSmartTLS(ctx, c, s) }()
		default:
			_ = c.Close()
		}
	}
}
func (a *App) smartClientFor(ip net.IP, name string) bool {
	a.dns.mu.Lock()
	defer a.dns.mu.Unlock()
	if !a.dns.data.Settings.Enabled || !a.dns.data.Settings.SmartEnabled {
		return false
	}
	for _, c := range a.dns.data.Clients {
		if dnsClientActive(c) && c.Smart && clientMatchesIP(c, ip) && gameDomainMatches(name, smartGameDomains(c)) {
			return true
		}
	}
	return false
}
func (a *App) handleSmartTLS(ctx context.Context, c net.Conn, s DNSSettings) {
	defer c.Close()
	_ = c.SetDeadline(time.Now().Add(5 * time.Second))
	hello, name, e := readTLSClientHello(c)
	if e != nil {
		return
	}
	var upstream net.Conn
	dialCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if strings.EqualFold(name, s.PanelHost) {
		upstream, e = (&net.Dialer{}).DialContext(dialCtx, "tcp", "127.0.0.1:7443")
	} else {
		host, _, _ := net.SplitHostPort(c.RemoteAddr().String())
		if !a.smartClientFor(net.ParseIP(host), name) {
			return
		}
		lookup := net.DefaultResolver.LookupIPAddr
		if a.dns.resolveIP != nil {
			lookup = a.dns.resolveIP
		}
		ips, err := lookup(dialCtx, name)
		if err != nil || len(ips) == 0 {
			return
		}
		for _, ip := range ips {
			if !publicNetworkIP(ip.IP) || ip.IP.Equal(net.ParseIP(s.SmartIP)) || localInterfaceIP(ip.IP) {
				return
			}
		}
		for _, ip := range ips {
			addr := net.JoinHostPort(ip.IP.String(), "443")
			if a.dns.dialTLS != nil {
				upstream, e = a.dns.dialTLS(dialCtx, addr)
			} else {
				upstream, e = (&net.Dialer{}).DialContext(dialCtx, "tcp", addr)
			}
			if e == nil {
				break
			}
		}
	}
	if e != nil || upstream == nil {
		return
	}
	defer upstream.Close()
	_ = upstream.SetDeadline(time.Now().Add(10 * time.Second))
	if _, e = upstream.Write(hello); e != nil {
		return
	}
	_ = c.SetDeadline(time.Time{})
	_ = upstream.SetDeadline(time.Time{})
	finished := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			_ = c.Close()
			_ = upstream.Close()
		case <-finished:
		}
	}()
	defer close(finished)
	done := make(chan struct{}, 1)
	go func() { relayIdleCopy(c, upstream); done <- struct{}{} }()
	relayIdleCopy(upstream, c)
	_ = upstream.Close()
	_ = c.Close()
	<-done
}
func localInterfaceIP(ip net.IP) bool {
	addrs, _ := net.InterfaceAddrs()
	for _, a := range addrs {
		s := strings.Split(a.String(), "/")[0]
		if ip.Equal(net.ParseIP(s)) {
			return true
		}
	}
	return false
}

// Deadlines are refreshed only on actual traffic, avoiding permanent idle sockets.
func relayIdleCopy(dst, src net.Conn) {
	buf := make([]byte, 32*1024)
	for {
		_ = src.SetReadDeadline(time.Now().Add(2 * time.Minute))
		n, e := src.Read(buf)
		if n > 0 {
			_ = dst.SetWriteDeadline(time.Now().Add(15 * time.Second))
			if _, err := dst.Write(buf[:n]); err != nil {
				return
			}
		}
		if e != nil {
			return
		}
	}
}

// Read the first handshake across TLS records, without terminating TLS or
// manufacturing certificates. Encrypted ClientHello cannot be routed by SNI.
func readTLSClientHello(r io.Reader) ([]byte, string, error) {
	var raw, handshake []byte
	for records := 0; records < 8; records++ {
		h := make([]byte, 5)
		if _, e := io.ReadFull(r, h); e != nil {
			return nil, "", e
		}
		n := int(binary.BigEndian.Uint16(h[3:]))
		if h[0] != 22 || h[1] != 3 || n == 0 || n > 18432 || len(raw)+n+5 > 65536 {
			return nil, "", fmt.Errorf("invalid ClientHello record")
		}
		body := make([]byte, n)
		if _, e := io.ReadFull(r, body); e != nil {
			return nil, "", e
		}
		raw = append(raw, h...)
		raw = append(raw, body...)
		handshake = append(handshake, body...)
		if len(handshake) < 4 {
			continue
		}
		if handshake[0] != 1 {
			return nil, "", fmt.Errorf("ClientHello required")
		}
		length := int(handshake[1])<<16 | int(handshake[2])<<8 | int(handshake[3])
		if length > 64000 {
			return nil, "", fmt.Errorf("ClientHello too large")
		}
		if len(handshake) < length+4 {
			continue
		}
		name, e := clientHelloSNI(handshake[4 : 4+length])
		return raw, name, e
	}
	return nil, "", fmt.Errorf("too many ClientHello records")
}
func clientHelloSNI(b []byte) (string, error) {
	invalid := fmt.Errorf("SNI absent, encrypted or invalid")
	if len(b) < 35 {
		return "", invalid
	}
	offset := 34
	advance := func(n int) bool {
		if n < 0 || offset+n > len(b) {
			return false
		}
		offset += n
		return true
	}
	n := int(b[offset])
	offset++
	if !advance(n) || offset+2 > len(b) {
		return "", invalid
	}
	n = int(binary.BigEndian.Uint16(b[offset:]))
	offset += 2
	if !advance(n) || offset >= len(b) {
		return "", invalid
	}
	n = int(b[offset])
	offset++
	if !advance(n) || offset+2 > len(b) {
		return "", invalid
	}
	n = int(binary.BigEndian.Uint16(b[offset:]))
	offset += 2
	if offset+n != len(b) {
		return "", invalid
	}
	name := ""
	for offset < len(b) {
		if offset+4 > len(b) {
			return "", invalid
		}
		kind := binary.BigEndian.Uint16(b[offset:])
		size := int(binary.BigEndian.Uint16(b[offset+2:]))
		offset += 4
		if offset+size > len(b) {
			return "", invalid
		}
		v := b[offset : offset+size]
		offset += size
		if kind == 0xfe0d {
			return "", invalid
		}
		if kind != 0 {
			continue
		}
		if name != "" || len(v) < 5 || int(binary.BigEndian.Uint16(v)) != len(v)-2 || v[2] != 0 || int(binary.BigEndian.Uint16(v[3:])) != len(v)-5 {
			return "", invalid
		}
		name = strings.ToLower(string(v[5:]))
	}
	if !validHostname(name) || net.ParseIP(name) != nil || !strings.Contains(name, ".") {
		return "", invalid
	}
	return name, nil
}
