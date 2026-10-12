# ariaatashin 4.7.0 — validation report

Date: 2026-10-12. Source package update from the confirmed complete 4.6 archive. No game account, customer hosting credentials or Iranian operator connection was used.

## Verified results

| Boundary/check | Result |
|---|---|
| Go unit/regression suite with race detector | PASS, 5.081 s |
| Full native/integration/regression suite with race detector | PASS, 18.773 s |
| go vet | PASS |
| Go build with buildvcs=false | PASS |
| Game catalog, DNS account policy, exit import and downloads in browser | PASS |
| Desktop 1440×1050 and mobile 390×844 | PASS; zero JavaScript errors, no horizontal overflow |
| Native sing-box import validation | PASS for VLESS WS, Trojan, Hysteria2, TUIC and AnyTLS |
| Native configuration checking of each catalog entry | PASS for full/private, full/independent-exit-DNS, and domain/split-DNS exports; Android package and Marvel Windows process exports also accepted |
| UDP over exported VLESS/XUDP | PASS directly and through real Node/Vercel and Deno relay templates |
| Explicitly selected additional exit | PASS; UDP reached native Xray while the unselected origin address was changed to unreachable |

The native selected-exit test uses an isolated, administrator-owned local TLS fixture and fixture CA. It proves selection and payload delivery, not a different geographic region or acceptance by a commercial game. Provider-runtime fixtures use HTTP listeners behind their platform TLS boundary while validating TLS to the actual origin. The existing Cloudflare fetch-handler DNS contract test is retained; no hosted account deployment was performed.

## DNS correctness and efficiency

- A deliberately delayed primary resolver was overtaken by the next resolver after the configured hedge delay; the winner's answer returned before the delayed primary. The loser was canceled and was not recorded as a failed resolver. No third resolver was queried after the fast winner.
- Twenty concurrent equivalent questions, including distinct DNS IDs and question capitalization, shared one upstream request. Each successful response retained the caller's ID and question. Canceling the first caller did not cancel the remaining clients' shared query.
- TTL aging, cache expiration, cache isolation after changing resolver settings, bounded LRU retention, and recently used response retention were exercised.
- Existing RFC8484 wire-format, ECS stripping, authenticated URL, quota/revocation/persistence, classic TCP/UDP ACL, TLS passthrough, ECH/bounds/fuzz-seed and incomplete unauthorized upload regression tests remain in the full suite.
- Resolver stats are copied under the lock before JSON serialization; race checks cover concurrent resolver updates and queries.

These are functional efficiency tests and controlled delayed-network tests. They do not establish a universal DNS latency figure or lower gameplay ping. The adaptive resolver's benchmark is from the server network; URLTest in game clients measures HTTP, not UDP match traffic.

## Game policy and credentials

- Catalog includes Clash of Clans, Rainbow Six, Call of Duty/Warzone, COD Mobile, Battlefield, Marvel Rivals, Steam and Epic. The publisher suffixes are starting lists; fixed-IP match coverage is provided by full mode or supported per-application routing.
- A direct COD Mobile policy does not include its package in the protected Android package list. Direct profiles are excluded from Smart DNS rewrites. Conflicting shared publisher domains retain protected routing precedence.
- Full mode keeps public TCP/UDP on the selected group and its DNS inside the tunnel. Domain mode keeps unrelated traffic direct. Split DNS gives unrelated questions to the local resolver; full mode rejects split DNS.
- Additional exit imports, private restart persistence, secret redaction, session-gated backup, and client selector/URLTest fields were checked. Native validation is distinct from connectivity and game acceptance.
- Private/loopback exit addresses, insecure TLS flags, XHTTP imports and TLS file-reading options are rejected. Supported inputs preserve administrator-supplied server credentials, which are only included in authenticated private backup/client export.
- Configurations keep existing connections uninterrupted when changing selection; new connections can use a newly selected exit. Independent-exit DNS avoids needing the panel origin for DNS resolution when that option is selected.

## Browser story

Fresh admin/admin login → adaptive DNS settings → named DNS account with game presets and a persisted direct COD Mobile policy → native connection user → eight game cards → private owned-exit URI import → Battlefield full-tunnel/auto-exit download with independent DNS → Clash Android application-only download → in-panel guide. Generated JSON contained the expected selector/URLTest, Cloudflare DNS through the selected exit and exact Clash Android package. No console/page errors or viewport overflow occurred.

## Remaining deployment/device checks

Actual login, downloads and multiplayer matches for these commercial games were not run. No claims are made about current Iran sanctions, account/store/payment region changes, universal unblocking or best ping. Windows process names depend on the installed edition; only the Marvel process names and Android package IDs sourced from official publisher/store information are prefilled. Other process/package entries can be supplied from the actual installation, or full-tunnel mode can be used.

Physical Android/iOS/PC TUN operation, console/router VPN sharing, NAT type, UDP reachability, ISP loss, exit-IP reputation and game account acceptance need testing on the installed device/server. Docker daemon was unavailable, so the full image/Compose stack was not built here; native app/engines and the previously validated optional Caddy config are provided. No production deployment, Telegram message or remote account mutation occurred.

## Reproduction

```bash
go test -race ./...
go vet ./...
go build -buildvcs=false .
ARIA_TEST_ENGINE_DIR=/path/to/pinned/x-ui \
ARIA_TEST_SING_BOX=/path/to/stats-enabled/sing-box \
ARIA_TEST_WS_MODULE=/path/to/node_modules/ws \
ARIA_TEST_DENO=/path/to/deno \
go test -race -tags=integration -count=1 ./...
```

Pinned engine/toolchain/dependencies from 4.6 remain unchanged. No new Go dependency was added for coalescing, adaptive resolver selection, profiles or exit management.
