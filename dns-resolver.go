package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"net/http"
	"sort"
	"time"

	"github.com/miekg/dns"
)

type DNSResolverStat struct {
	Provider      string  `json:"provider"`
	LatencyMS     float64 `json:"latencyMS"`
	Successes     uint64  `json:"successes"`
	Failures      uint64  `json:"failures"`
	LastAt        int64   `json:"lastAt"`
	CooldownUntil int64   `json:"cooldownUntil"`
}
type dnsFlight struct {
	done    chan struct{}
	cancel  context.CancelFunc
	waiters int
	answer  *dns.Msg
	err     error
}

func resolverMode(s DNSSettings) string {
	if s.ResolverMode == "ordered" {
		return "ordered"
	}
	return "adaptive"
}
func resolverHedge(s DNSSettings) time.Duration {
	v := s.HedgeMS
	if v == 0 {
		v = 180
	}
	return time.Duration(v) * time.Millisecond
}
func resolverKey(q *dns.Msg, s DNSSettings) string {
	b, _ := json.Marshal([]any{s.Upstreams, resolverMode(s), s.TimeoutMS, s.HedgeMS, s.Cache})
	return string(b) + dnsCacheKey(q)
}
func (a *App) resolveDoH(ctx context.Context, q *dns.Msg, s DNSSettings) (*dns.Msg, error) {
	d := a.dns
	key := resolverKey(q, s)
	now := time.Now()
	d.mu.Lock()
	if s.Cache {
		if cached, ok := d.cache[key]; ok && now.Before(cached.Expires) {
			msg := cached.Message.Copy()
			elapsed := uint32(now.Sub(cached.Added) / time.Second)
			for _, section := range [][]dns.RR{msg.Answer, msg.Ns, msg.Extra} {
				for _, rr := range section {
					if rr.Header().Rrtype != dns.TypeOPT {
						rr.Header().Ttl -= min(rr.Header().Ttl, elapsed)
					}
				}
			}
			cached.Accessed = now
			d.cache[key] = cached
			d.hits++
			d.mu.Unlock()
			return dnsReplyFor(msg, q), nil
		}
	}
	f, exists := d.flights[key]
	if exists {
		f.waiters++
		d.coalesced++
	} else {
		if len(d.flights) >= 64 {
			d.mu.Unlock()
			return nil, fmt.Errorf("DNS همزمان بیش از ظرفیت است")
		}
		// A disconnected caller must not cancel another client's shared request.
		budget := time.Duration(s.TimeoutMS)*time.Millisecond + 2*resolverHedge(s) + time.Second
		if resolverMode(s) == "ordered" {
			budget = time.Duration(s.TimeoutMS*len(s.Upstreams))*time.Millisecond + time.Second
		}
		flightCtx, cancel := context.WithTimeout(context.Background(), budget)
		f = &dnsFlight{done: make(chan struct{}), cancel: cancel, waiters: 1}
		d.flights[key] = f
		query := q.Copy()
		go func() {
			answer, e := a.resolveDNSNetwork(flightCtx, query, s)
			cancel()
			d.mu.Lock()
			defer d.mu.Unlock()
			f.answer = answer
			f.err = e
			if e == nil && s.Cache && f.waiters > 0 {
				d.storeDNSCache(key, answer)
			}
			if d.flights[key] == f {
				delete(d.flights, key)
			}
			close(f.done)
		}()
	}
	d.mu.Unlock()
	defer func() {
		d.mu.Lock()
		defer d.mu.Unlock()
		f.waiters--
		if f.waiters == 0 {
			f.cancel()
			if d.flights[key] == f {
				delete(d.flights, key)
			}
		}
	}()
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-f.done:
		if f.err != nil {
			return nil, f.err
		}
		return dnsReplyFor(f.answer.Copy(), q), nil
	}
}
func dnsReplyFor(answer, q *dns.Msg) *dns.Msg {
	answer.Id = q.Id
	answer.Question = append([]dns.Question{}, q.Question...)
	return answer
}

// Caller holds mu. Evict expired entries first, then the least recently used.
func (d *DNSRuntime) storeDNSCache(key string, answer *dns.Msg) {
	if answer.Truncated {
		return
	}
	ttl := uint32(300)
	found := false
	for _, rr := range append(append([]dns.RR{}, answer.Answer...), answer.Ns...) {
		found = true
		v := rr.Header().Ttl
		if soa, ok := rr.(*dns.SOA); ok {
			v = min(v, soa.Minttl)
		}
		ttl = min(ttl, v)
	}
	if !found || ttl == 0 {
		return
	}
	now := time.Now()
	if len(d.cache) >= 1024 {
		oldest := ""
		var oldestAt time.Time
		for k, v := range d.cache {
			if now.After(v.Expires) {
				delete(d.cache, k)
				continue
			}
			if oldest == "" || v.Accessed.Before(oldestAt) {
				oldest = k
				oldestAt = v.Accessed
			}
		}
		if len(d.cache) >= 1024 {
			delete(d.cache, oldest)
		}
	}
	d.cache[key] = dnsCacheEntry{Message: answer.Copy(), Added: now, Expires: now.Add(time.Duration(ttl) * time.Second), Accessed: now}
}
func (d *DNSRuntime) upstreamOrder(s DNSSettings) []string {
	order := append([]string{}, s.Upstreams...)
	if resolverMode(s) == "ordered" {
		return order
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	now := time.Now().UnixMilli()
	sort.SliceStable(order, func(i, j int) bool {
		a, b := d.resolverStats[order[i]], d.resolverStats[order[j]]
		ac, bc := a.CooldownUntil > now, b.CooldownUntil > now
		if ac != bc {
			return !ac
		}
		if a.Successes == 0 || b.Successes == 0 {
			return a.Successes > 0 && b.Successes == 0
		}
		return a.LatencyMS < b.LatencyMS
	})
	return order
}
func (d *DNSRuntime) recordResolver(name string, elapsed time.Duration, err error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	v := d.resolverStats[name]
	v.Provider = name
	v.LastAt = time.Now().UnixMilli()
	if err == nil {
		ms := float64(elapsed.Microseconds()) / 1000
		if v.Successes == 0 {
			v.LatencyMS = ms
		} else {
			v.LatencyMS = .75*v.LatencyMS + .25*ms
		}
		v.Successes++
		v.CooldownUntil = 0
	} else {
		v.Failures++
		v.CooldownUntil = time.Now().Add(20 * time.Second).UnixMilli()
	}
	d.resolverStats[name] = v
}
func (a *App) queryDNSUpstream(ctx context.Context, q *dns.Msg, s DNSSettings, name string) (*dns.Msg, error) {
	attempt, cancel := context.WithTimeout(ctx, time.Duration(s.TimeoutMS)*time.Millisecond)
	defer cancel()
	select {
	case a.dns.fetchSlots <- struct{}{}:
		defer func() { <-a.dns.fetchSlots }()
	case <-attempt.Done():
		return nil, attempt.Err()
	}
	start := time.Now()
	answer, err := a.fetchDNSUpstream(attempt, q, name)
	// A canceled hedge loser is not a failed resolver and must not be cooled down.
	if ctx.Err() == nil {
		a.dns.recordResolver(name, time.Since(start), err)
	}
	return answer, err
}
func (a *App) fetchDNSUpstream(ctx context.Context, q *dns.Msg, name string) (*dns.Msg, error) {
	wire, _ := q.Pack()
	cl := a.dns.http
	if cl == nil {
		cl = &http.Client{Timeout: 9 * time.Second, CheckRedirect: noRedirect}
	}
	req, _ := http.NewRequestWithContext(ctx, "POST", dohUpstreams[name], bytes.NewReader(wire))
	req.Header.Set("Content-Type", "application/dns-message")
	req.Header.Set("Accept", "application/dns-message")
	req.Header.Set("User-Agent", "")
	resp, e := cl.Do(req)
	if e != nil {
		if ctx.Err() != nil {
			return nil, fmt.Errorf("%s: مهلت DNS تمام شد", name)
		}
		return nil, fmt.Errorf("%s: شبکه یا TLS پاسخ نداد", name)
	}
	defer resp.Body.Close()
	b, e := io.ReadAll(io.LimitReader(resp.Body, 65536))
	ct, _, _ := mime.ParseMediaType(resp.Header.Get("Content-Type"))
	answer := new(dns.Msg)
	if resp.StatusCode != 200 || ct != "application/dns-message" || e != nil || len(b) > 65535 || answer.Unpack(b) != nil || !answer.Response || answer.Id != q.Id || len(answer.Question) != 1 || !sameDNSQuestion(answer.Question[0], q.Question[0]) {
		return nil, fmt.Errorf("%s: پاسخ DNS معتبر نیست (HTTP %d)", name, resp.StatusCode)
	}
	if answer.Rcode == dns.RcodeServerFailure || answer.Rcode == dns.RcodeRefused {
		return nil, fmt.Errorf("%s: %s", name, dns.RcodeToString[answer.Rcode])
	}
	return answer, nil
}
func sameDNSQuestion(a, b dns.Question) bool {
	return dnsCacheKey(&dns.Msg{Question: []dns.Question{a}}) == dnsCacheKey(&dns.Msg{Question: []dns.Question{b}})
}
func (a *App) resolveDNSNetwork(parent context.Context, q *dns.Msg, s DNSSettings) (*dns.Msg, error) {
	order := a.dns.upstreamOrder(s)
	last := fmt.Errorf("resolver HTTPS در دسترس نیست")
	if resolverMode(s) == "ordered" {
		for _, name := range order {
			answer, e := a.queryDNSUpstream(parent, q, s, name)
			if e == nil {
				return answer, nil
			}
			last = e
		}
		return nil, last
	}
	ctx, cancel := context.WithCancel(parent)
	defer cancel()
	type result struct {
		answer *dns.Msg
		err    error
	}
	results := make(chan result, len(order))
	next, active := 0, 0
	start := func() {
		name := order[next]
		next++
		active++
		go func() { answer, e := a.queryDNSUpstream(ctx, q, s, name); results <- result{answer, e} }()
	}
	if len(order) == 0 {
		return nil, last
	}
	start()
	timer := time.NewTimer(resolverHedge(s))
	defer timer.Stop()
	for active > 0 {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-timer.C:
			if next < len(order) {
				start()
				timer.Reset(resolverHedge(s))
			}
		case r := <-results:
			active--
			if r.err == nil {
				return r.answer, nil
			}
			last = r.err
			if next < len(order) {
				start()
			}
		}
	}
	return nil, last
}
