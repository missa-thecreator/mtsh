#!/bin/sh
# Gera os pacotes do mtsh pra cada sistema em dist/.
#
#   ./scripts/build.sh            # versão = tag do git (ou "dev")
#   ./scripts/build.sh v0.1.0     # versão explícita
#
# Cada pacote é um .tar.gz com o binário (as fontes dos ícones já vão
# embutidas nele), o README e as licenças. Também gera checksums.txt.
set -eu

cd "$(dirname "$0")/.."

VERSION="${1:-$(git describe --tags --always 2>/dev/null || echo dev)}"
TARGETS="linux/amd64 linux/arm64 darwin/amd64 darwin/arm64"

rm -rf dist
mkdir -p dist

for target in $TARGETS; do
	os="${target%/*}"
	arch="${target#*/}"
	name="mtsh_${VERSION#v}_${os}_${arch}"
	work="dist/$name"
	mkdir -p "$work"

	echo "→ $os/$arch"
	CGO_ENABLED=0 GOOS="$os" GOARCH="$arch" GOTOOLCHAIN=local \
		go build -trimpath -ldflags "-s -w -X main.version=$VERSION" -o "$work/mtsh" .

	cp README.md LICENSE "$work/"
	cp internal/fonts/LICENSE-cascadia internal/fonts/LICENSE-nerd-fonts "$work/"
	tar -C dist -czf "dist/$name.tar.gz" "$name"
	rm -rf "$work"
done

cp install.sh dist/
(cd dist && sha256sum ./*.tar.gz | sed 's| \./| |' > checksums.txt)

echo
echo "Pronto ($VERSION):"
ls -lh dist
