package main

import (
	"context"
	"sort"
	"sync"
	"time"

	"github.com/miekg/dns"
)

type DNSBenchmark struct {
	Provider  string `json:"provider"`
	OK        bool   `json:"ok"`
	MS        int64  `json:"ms"`
	Samples   int    `json:"samples"`
	Successes int    `json:"successes"`
	Rcode     string `json:"rcode,omitempty"`
	Answers   int    `json:"answers"`
	Error     string `json:"error,omitempty"`
}

// Three measurements per resolver are run concurrently across providers.
// The median reduces the influence of a cold TLS connection. There is no
// background game-domain history or diagnosis of a player's account region.
func (a *App) benchmarkDNS(ctx context.Context, name string, s DNSSettings) []DNSBenchmark {
	results := make([]DNSBenchmark, len(s.Upstreams))
	var wg sync.WaitGroup
	for i, provider := range s.Upstreams {
		wg.Add(1)
		go func(i int, provider string) {
			defer wg.Done()
			v := DNSBenchmark{Provider: provider, Samples: 3}
			times := []int64{}
			one := s
			one.Upstreams = []string{provider}
			one.Cache = false
			one.TimeoutMS = max(s.TimeoutMS, 8000)
			for n := 0; n < 3; n++ {
				if ctx.Err() != nil {
					break
				}
				q := new(dns.Msg)
				q.SetQuestion(dns.Fqdn(name), dns.TypeA)
				start := time.Now()
				answer, e := a.resolveDoH(ctx, q, one)
				if e != nil {
					v.Error = e.Error()
					continue
				}
				times = append(times, time.Since(start).Milliseconds())
				v.Rcode = dns.RcodeToString[answer.Rcode]
				v.Answers = len(answer.Answer)
			}
			v.Successes = len(times)
			v.OK = len(times) > 0
			if v.OK {
				sort.Slice(times, func(i, j int) bool { return times[i] < times[j] })
				v.MS = times[len(times)/2]
				v.Error = ""
				a.dns.mu.Lock()
				stat := a.dns.resolverStats[provider]
				stat.LatencyMS = float64(v.MS)
				stat.CooldownUntil = 0
				a.dns.resolverStats[provider] = stat
				a.dns.mu.Unlock()
			}
			results[i] = v
		}(i, provider)
	}
	wg.Wait()
	return results
}
func (a *App) dnsResolverMonitor(ctx context.Context) {
	t := time.NewTicker(5 * time.Minute)
	defer t.Stop()
	for {
		s := a.dns.dataSettings()
		if s.Enabled && resolverMode(s) == "adaptive" {
			c, cancel := context.WithTimeout(ctx, 28*time.Second)
			_ = a.benchmarkDNS(c, "example.com", s)
			cancel()
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		case <-a.dns.resolverWake:
		}
	}
}
