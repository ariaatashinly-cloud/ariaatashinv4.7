#!/usr/bin/env bash
set -euo pipefail
cd -- "$(dirname -- "${BASH_SOURCE[0]}")"
umask 077
[[ "$(uname -s)" == Linux && "$(uname -m)" == x86_64 ]] || { echo 'Linux amd64 is required.'; exit 1; }
command -v docker >/dev/null || { echo 'Install Docker Engine and its Compose plugin first.'; exit 1; }
docker compose version >/dev/null
if [[ ! -f .env ]]; then
 read -r -p 'Domain pointing to this server (vpn.example.com): ' aria_domain
 aria_domain="${aria_domain,,}"
 [[ "$aria_domain" =~ ^[a-z0-9][a-z0-9.-]*\.[a-z]{2,63}$ && "$aria_domain" != *..* ]] || { echo 'Invalid domain.'; exit 1; }
 {
  printf 'ARIA_DOMAIN=%s\n' "$aria_domain"
  printf 'ARIA_MODE=vps\n'
 } > .env
 chmod 0600 .env
 echo 'Domain settings saved. First login: admin / admin; change it in panel settings.'
else
 echo 'Existing .env retained.'
fi
docker compose -p ariaatashin -f compose.vps.yaml config --quiet
docker compose -p ariaatashin -f compose.vps.yaml up -d --build
echo 'Deployment started. Open https://YOUR_DOMAIN after DNS and TLS are ready.'
echo 'Allow TCP 80/443 and only the direct TCP/UDP ports you use. See INSTALL-VPS-FA.md.'
