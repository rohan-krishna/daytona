#!/bin/bash
set -e

if [[ "$OSTYPE" != "darwin"* ]]; then
  echo "This script is for macOS only."
  exit 1
fi

command -v brew >/dev/null || {
  echo "Homebrew is required. Install Homebrew first."
  exit 1
}

command -v dnsmasq >/dev/null || brew install dnsmasq

BREW_PREFIX="$(brew --prefix)"
DNSMASQ_CONF="$BREW_PREFIX/etc/dnsmasq.conf"

sudo mkdir -p "$BREW_PREFIX/etc" /etc/resolver

if ! grep -q "address=/proxy.localhost/127.0.0.1" "$DNSMASQ_CONF" 2>/dev/null; then
  echo "address=/proxy.localhost/127.0.0.1" | sudo tee -a "$DNSMASQ_CONF"
fi

echo "nameserver 127.0.0.1" | sudo tee /etc/resolver/proxy.localhost

sudo brew services restart dnsmasq

sudo dscacheutil -flushcache
sudo killall -HUP mDNSResponder || true

echo "Done."
echo "Test with:"
echo "dig 2280-test.proxy.localhost"
