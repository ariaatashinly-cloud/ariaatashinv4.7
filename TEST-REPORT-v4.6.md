# ariaatashin 4.6.0 — validation report

Date: 2026-10-12. This report describes tests of the source package. No customer's hosting account, Telegram bot, production DNS records or game account was used.

## Results

| Check | Result |
|---|---|
| Go unit/regression suite with race detector | PASS, 3.781 s |
| Complete integration/regression suite with race detector | PASS, 34.229 s |
| go vet | PASS |
| Go build, buildvcs=false | PASS |
| VPS installer shell syntax | PASS; fresh installs ask only for the domain, default admin/admin |
| Browser on desktop 1440×1050 and mobile 390×844 | PASS, zero JavaScript errors, no horizontal overflow |
| Caddy 2.11.4 adaptation and config validation | PASS; HTTP80, loopback TLS7443, h1/h2, no advertised HTTP3 |
| ClientHello parser fuzzing | PASS, 60,337 executions in 3 seconds |
| Live RFC8484 HTTPS DNS: Cloudflare, Google, Quad9 | PASS, all three; 18.230 s total with explicit 8 s per-resolver budget |

The live resolver checks run from this environment's network through its configured outbound HTTPS proxy; the resolver client now respects standard HTTP_PROXY/HTTPS_PROXY settings while still verifying TLS and disallowing redirects. This is not an Iranian operator test. The panel's ordinary resolver timeout is adjustable; the live test used the larger budget for this environment.

## DNS behavior exercised

- Actual DNS wire-format GET and POST at a private 48-hex-character path; `application/dns-message`, `no-store`, ID/question matching and parsing limits.
- Private state redaction, session-gated export and backup APIs, quota enforcement, expiry/disabled access checks, key rotation, restart persistence and additive backup restore without opening listeners or replacing existing accounts.
- EDNS Client Subnet stripping; fresh-query forwarding without padding/cookies; bounded TTL cache; fallback after SERVFAIL; rejection of ANY/AXFR/IXFR and multi-question messages.
- Real TCP and UDP DNS listeners on isolated test ports, active-client IP authorization and revocation. Production API validation rejects unrestricted/private source CIDRs.
- Unknown DoH credentials rejected before waiting for a body; incomplete unauthorized HTTP/1 upload regression.
- Selected A rewrite to relay IPv4; selected AAAA/HTTPS/SVCB empty responses; domain suffix boundaries; no false DNSSEC AD flag; no Smart answer before the TLS relay is listening.
- Actual encrypted HTTPS request and response through the SNI gateway. The client verifies the original destination certificate, which remains byte-identical across the gateway. Revoked source access fails.
- Fragmented ClientHello records, explicit ECH rejection, oversize bounds and malformed-input fuzzing.
- Public-address checks reject private, loopback, metadata/CGNAT, documentation, benchmark and special-use networks.

## Relay and game traffic

- DNS GET/POST passed through the actual Vercel Node+ws and Deno templates to a TLS origin; management APIs stayed inaccessible through the relay.
- Cloudflare Worker `fetch` handler carried actual binary DNS requests/responses using Node Request/Response/fetch locally. The test adapter supplies Node's streaming `duplex` setting. This verifies the template's HTTP forwarding logic, not a hosted Cloudflare deployment or its WebSocket runtime.
- Old deployed relay versions cannot be exported as supporting DoH merely because the origin panel was upgraded. New token-created routes are tagged as having the DNS-capable template.
- Both game-only and full-tunnel exports were accepted by the real sing-box 1.14.3 configuration checker.
- The exported VLESS XUDP outbound carried an actual UDP echo payload through WebSocket to native Xray, directly and through the actual Node/Vercel and Deno relay templates. Local provider-runtime listeners use HTTP as the fixture for the provider's internally terminated HTTPS; TLS to the origin is validated with the fixture CA.
- Regression tests continue to exercise real native Xray TCP traffic, XHTTP HTTP/1 full-duplex streaming, manual profiles, restart/backup recovery, independent sing-box traffic accounting and prior receipt/Telegram ownership and QR behavior.

## Browser flows

Verified initial admin/admin login, enabling DoH, creating a named personal account with Steam/custom domains, exporting the private URL and SVG QR, each provider tutorial, tutorial inside the provider form, creating a native connection user and downloading a full TUN game configuration. The DNS QR was also decoded from the browser screenshot with ZXing and yielded the correct 48-character private-key path. Existing themes and panel components are retained. Smart DNS tutorial was checked on mobile; its Compose command has a separate copyable block.

## Scope and limits

Docker daemon was unavailable, so the full Docker image/Compose stack was not built or launched here. The app and pinned native engines were executed directly; the optional Caddy config was validated with the real pinned Caddy binary. ACME issuance, VPS firewall/NAT, public ports 53/443, hosted provider deployments with customer tokens, physical-device TUN permissions and access to commercial game accounts require testing on the installed server/device.

No national-shutdown or Iranian ISP experiment was performed. Smart DNS is selective HTTPS/SNI relay, not a generic UDP game tunnel; account/store/payment region checks may still apply. DoH alone does not change the game traffic's exit IP. No newly invented protocol or guarantee of unfilterability is claimed.

## Reproduction

```bash
go test -race ./...
go vet ./...
go build -buildvcs=false .

# Set paths to the fixed native engines and test runtimes:
ARIA_TEST_ENGINE_DIR=/path/to/x-ui \
ARIA_TEST_SING_BOX=/path/to/stats-enabled/sing-box \
ARIA_TEST_WS_MODULE=/path/to/node_modules/ws \
ARIA_TEST_DENO=/path/to/deno \
go test -race -tags=integration -count=1 ./...

# Optional external-network DNS test:
ARIA_TEST_LIVE_DNS=true go test -tags=integration -run TestLiveHTTPSDNS46 -count=1 -v ./...
go test -run '^$' -fuzz FuzzTLSClientHello46 -fuzztime=3s -parallel=2
```

Build dependencies: Go ≥1.25; toolchain/build image 1.26.9, miekg/dns 1.1.73, official pinned 3x-ui 3.8.5 / Xray 26.9.9, patched managed sing-box 1.14.3 with QUIC/uTLS/V2Ray stats, Node ws 8.22.0 and Deno 2.9.6. Third-party Go DNS/network license files are included under `licenses/`.
