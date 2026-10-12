# Verification report — ariaatashin 4.5.0

Date: 2026-10-12. This report distinguishes local execution evidence from untested account deployments and Iranian operator reachability.

## Complete flows verified

| Flow | Evidence |
|---|---|
| Default login and advanced config creation | Browser login with admin/admin, existing native Xray create flow and advanced configuration fixtures pass |
| Country lookup → persisted cache → user links | Mock HTTPS lookup, real cache round trip, VLESS/VMess titles, stale-cache preservation on API failure and no duplicate location title on country change |
| Country UI → local SVG | Browser renders the Persian country name; local flag image loads with nonzero natural width |
| Telegram delivery → ownership → PNG | Separate text and photo notices for subscription/configs; real PNG decode; numeric recipient checked; state survives restart; wrong customer rejected |
| CDN output → Telegram owner | Registered local service owner selected from sales data; route URI generated; disabled/revoked route cannot be delivered |
| QR card → independent scan | ZXing-C++ recovered the complete payload, including country flag and Persian name, exactly |
| Vercel adapter | Mock API asserts project creation and production deployment files; origin present; management token absent from relay source and public response |
| Deno adapter | Mock API asserts v2 App + Revision payload, `kind: file` assets and production target; Deno 2.9.6 type check passes |
| Cloudflare Worker adapter | Mock API asserts Worker upload, JS module/JSON multipart content types, account subdomain and workers.dev enable operation |
| HTTP/1 streaming response | Regression test fails before the change because response headers wait for upload completion; passes after enabling full duplex on tunnel POSTs |
| HTTP-only CDN rejection | HTTP 200 alone does not satisfy WebSocket handshake; Sec-WebSocket-Accept is checked |
| Real Node relay → TLS origin → Xray → target | Native Xray client sends an HTTP request through the exact Vercel relay source; expected target response recovered; admin path returns 404 |
| Real Deno relay → TLS origin → Xray → target | Native Xray traffic passes through the exact Deno relay source under Deno 2.9.6; TLS verification stays enabled with a valid fixture CA/leaf chain |
| Failover export syntax | Generated multi-route JSON with country-labelled tags is accepted by the actual Xray parser |
| Independent sing-box | Existing actual VLESS/WS tunnel, native traffic statistics and quota-enforcement integration tests retained and passed |
| Themes and wallpaper | Aurora persists on reload; Spider-Man/Light choices work; user-selected local image loads and can be cleared |
| Mobile UI | 390×844 viewport; no horizontal page overflow or JavaScript page errors |

The full suites also retain the account-change/session invalidation, receipt-photo proxy/ownership, sales/receipt/order flow, manual preview, restart recovery and managed-core backup regression coverage from 4.4.

Final results: race suite passed (4.415s); complete native integration suite passed (30.005s); go vet and app build exited successfully. The final browser run reports no page errors and no mobile overflow, with country URI, local flag, theme persistence, custom wallpaper and provider validation checks passing.

## Commands

```sh
go test -race ./...
go vet ./...
go test -race -tags=integration -count=1 ./...
go build -buildvcs=false .
node --check web/appearance.js
node --check web/connectivity.js
deno check <rendered-relay-template.ts>
```

Integration fixtures use the pinned 3x-ui 3.8.5/Xray engine, the managed sing-box compatibility build, ws 8.22.0 and Deno 2.9.6. Set `ARIA_TEST_ENGINE_DIR`, `ARIA_TEST_SING_BOX`, `ARIA_TEST_WS_MODULE` (path to the installed ws package), and `ARIA_TEST_DENO` to include those native fixtures; absent native dependencies cause explicit skips. The browser harness starts the application and Chromium in the same local network namespace.

Temporary browser data, test screenshots, test executables and fake account data are not shipped. Test sources and this report are included.

## Not verified

- No personal Vercel/Deno/Cloudflare account token was supplied. Real remote builds, project protection, billing/quotas and provider network behaviour were not tested on a user account. API tests are mocks; Node/Deno relay execution tests are local.
- Cloudflare Worker was not executed in the live Workers runtime; its multipart/API contract and source syntax were checked.
- Telegram delivery uses mocked Bot API responses. A private real bot/customer chat was not contacted.
- No Iranian operator or live shutdown was available for testing. Server probes do not prove client/operator reachability, throughput or unfilterability.
- Docker image was not built here because a Docker daemon is unavailable. App build, native engine checks and sing-box execution passed; the Dockerfile copies new `go.sum`, relay sources and web assets.
- Batman/Spider-Man character artwork generation was rejected by the image service. The package contains their color themes and local-image selection, plus original Aurora artwork.
