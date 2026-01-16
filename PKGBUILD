# Maintainer: warp-dl Developer <dev@example.com>
pkgname=warp-dl
pkgver=0.1.0
pkgrel=1
pkgdesc="High-performance multi-threaded download manager for CLI"
arch=('x86_64' 'aarch64')
url="https://github.com/muhamad-bari/warp-dl"
license=('MIT')
makedepends=('go')
source=() # Local build, sources assumed present or handled manually
md5sums=()

build() {
  # Build from the current directory (assuming GOPATH/Modules setup or local checkout)
  # In a real AUR package, we would cd into "$srcdir/$pkgname-$pkgver"

  export CGO_ENABLED=0
  go build \
    -trimpath \
    -ldflags "-s -w" \
    -o warp-dl \
    ./cmd/warp-dl
}

package() {
  install -Dm755 warp-dl "$pkgdir/usr/bin/warp-dl"
  install -Dm644 LICENSE "$pkgdir/usr/share/licenses/$pkgname/LICENSE"
}
