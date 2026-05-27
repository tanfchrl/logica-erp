#!/usr/bin/env bash
# Logica ERP — one-shot installer for a fresh Ubuntu 22.04/24.04 VPS.
# Idempotent: safe to re-run.

set -euo pipefail

REPO_URL="${REPO_URL:-}"            # set to your git remote, or leave blank to install from current dir
INSTALL_DIR="${INSTALL_DIR:-/opt/logica-erp}"
DOMAIN="${LOGICA_DOMAIN:-}"
ACME_EMAIL="${ACME_EMAIL:-}"

require_root() {
  if [ "$EUID" -ne 0 ]; then
    echo "Please run as root (sudo $0)" >&2
    exit 1
  fi
}

require_input() {
  if [ -z "$DOMAIN" ]; then
    read -rp "Public domain for this install (e.g. erp.example.com): " DOMAIN
  fi
  if [ -z "$ACME_EMAIL" ]; then
    read -rp "Email for Let's Encrypt: " ACME_EMAIL
  fi
}

install_docker() {
  if command -v docker >/dev/null 2>&1; then
    echo "✓ docker already installed"
    return
  fi
  apt-get update -qq
  apt-get install -y -qq ca-certificates curl gnupg
  install -m 0755 -d /etc/apt/keyrings
  curl -fsSL https://download.docker.com/linux/ubuntu/gpg | gpg --dearmor -o /etc/apt/keyrings/docker.gpg
  chmod a+r /etc/apt/keyrings/docker.gpg
  echo "deb [arch=$(dpkg --print-architecture) signed-by=/etc/apt/keyrings/docker.gpg] https://download.docker.com/linux/ubuntu $(. /etc/os-release && echo $VERSION_CODENAME) stable" > /etc/apt/sources.list.d/docker.list
  apt-get update -qq
  apt-get install -y -qq docker-ce docker-ce-cli containerd.io docker-buildx-plugin docker-compose-plugin
  systemctl enable --now docker
}

fetch_source() {
  if [ -n "$REPO_URL" ]; then
    if [ ! -d "$INSTALL_DIR/.git" ]; then
      git clone "$REPO_URL" "$INSTALL_DIR"
    else
      git -C "$INSTALL_DIR" pull --ff-only
    fi
  else
    mkdir -p "$INSTALL_DIR"
    rsync -a --delete --exclude data --exclude .env "$(dirname "$(readlink -f "$0")")/.." "$INSTALL_DIR/"
  fi
}

write_env() {
  local env="$INSTALL_DIR/.env"
  if [ -f "$env" ]; then
    echo "✓ $env already exists; leaving untouched"
    return
  fi
  cp "$INSTALL_DIR/.env.example" "$env"
  local secret
  secret="$(openssl rand -base64 64 | tr -d '\n')"
  sed -i "s|LOGICA_JWT_SECRET=.*|LOGICA_JWT_SECRET=$secret|" "$env"
  {
    echo "LOGICA_DOMAIN=$DOMAIN"
    echo "ACME_EMAIL=$ACME_EMAIL"
    echo "POSTGRES_PASSWORD=$(openssl rand -base64 32 | tr -d '\n=' )"
  } >> "$env"
  chmod 600 "$env"
}

bring_up() {
  cd "$INSTALL_DIR"
  docker compose -f deploy/docker-compose.yml --env-file .env up -d
  echo "Waiting for API to become ready…"
  for _ in {1..60}; do
    if docker compose -f deploy/docker-compose.yml exec -T api wget -qO- http://localhost:8080/readyz >/dev/null 2>&1; then
      echo "✓ API ready"
      break
    fi
    sleep 2
  done
  docker compose -f deploy/docker-compose.yml exec -T api /app/logica migrate up
  docker compose -f deploy/docker-compose.yml exec -T api /app/logica seed
}

main() {
  require_root
  require_input
  install_docker
  fetch_source
  write_env
  bring_up
  echo ""
  echo "Logica ERP is up at https://$DOMAIN"
  echo "Admin credentials are in $INSTALL_DIR/.env (LOGICA_BOOTSTRAP_ADMIN_*). Change the password on first login."
}

main "$@"
