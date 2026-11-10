#!/bin/sh
set -eu

CUSTOM_CA_DIR="/custom-ca"
INSTALLED_FLAG="/tmp/custom-ca-installed"

install_custom_cas() {
  if [ ! -d "$CUSTOM_CA_DIR" ]; then
    return 0
  fi

  found=0
  for cert in "$CUSTOM_CA_DIR"/*.crt; do
    if [ ! -f "$cert" ]; then
      continue
    fi

    found=1
    cp "$cert" "/usr/local/share/ca-certificates/$(basename "$cert")"
  done

  if [ "$found" -eq 1 ] && [ ! -f "$INSTALLED_FLAG" ]; then
    update-ca-certificates
    touch "$INSTALLED_FLAG"
  fi
}

if [ "$(id -u)" -eq 0 ]; then
  install_custom_cas
  gosu app /app/migrate
  exec gosu app /app/workspace
fi

/app/migrate
exec /app/workspace
