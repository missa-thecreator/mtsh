#!/bin/sh
# Instalador do mtsh.
#
# Última versão publicada:
#   curl -fsSL https://raw.githubusercontent.com/missa-thecreator/mtsh/main/install.sh | sh
#
# De um pacote baixado das Releases (sem internet):
#   ./install.sh --from mtsh_0.2.0_linux_amd64.tar.gz
#
# Variáveis opcionais:
#   MTSH_VERSION=v0.2.0        versão específica (padrão: a mais recente)
#   MTSH_INSTALL_DIR=...       onde instalar (padrão: ~/.local/bin)
#   MTSH_ICONS=sim|nao         instalar os ícones sem perguntar
#   MTSH_REPO=dono/repo        repositório de onde baixar (padrão: missa-thecreator/mtsh)
set -eu

REPO="${MTSH_REPO:-missa-thecreator/mtsh}"
API="${MTSH_GITHUB_API:-https://api.github.com}"
INSTALL_DIR="${MTSH_INSTALL_DIR:-$HOME/.local/bin}"

say() { printf '%s\n' "$*"; }
die() { printf 'mtsh: %s\n' "$*" >&2; exit 1; }

# ---- sistema ----
case "$(uname -s)" in
	Linux) os=linux ;;
	Darwin) os=darwin ;;
	*) die "sistema não suportado: $(uname -s). No Windows, use o mtsh dentro do WSL." ;;
esac
case "$(uname -m)" in
	x86_64 | amd64) arch=amd64 ;;
	aarch64 | arm64) arch=arm64 ;;
	*) die "processador não suportado: $(uname -m)" ;;
esac

tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT

# ---- pacote: local ou baixado ----
if [ "${1:-}" = "--from" ]; then
	[ -n "${2:-}" ] || die "uso: ./install.sh --from arquivo.tar.gz"
	[ -f "$2" ] || die "arquivo não encontrado: $2"
	pkg="$2"
	say "→ instalando de $pkg"
else
	command -v curl >/dev/null 2>&1 || die "precisa do curl pra baixar"
	if [ -n "${MTSH_VERSION:-}" ]; then
		release="$(curl -fsSL "$API/repos/$REPO/releases/tags/$MTSH_VERSION")" || die "versão $MTSH_VERSION não encontrada"
	else
		release="$(curl -fsSL "$API/repos/$REPO/releases/latest")" || die "não achei nenhuma versão publicada em github.com/$REPO"
	fi
	# links de download dos arquivos da versão (aceita "a":"b" e "a": "b")
	urls="$(printf '%s' "$release" | grep -o '"browser_download_url": *"[^"]*"' | sed 's/.*"\([^"]*\)"$/\1/')"
	pkg_url="$(printf '%s\n' "$urls" | grep "_${os}_${arch}\.tar\.gz$" | head -n1)"
	sums_url="$(printf '%s\n' "$urls" | grep '/checksums\.txt$' | head -n1)"
	[ -n "$pkg_url" ] || die "a versão publicada não tem pacote pra $os/$arch"

	pkg="$tmp/$(basename "$pkg_url")"
	say "→ baixando $(basename "$pkg_url")"
	curl -fsSL -o "$pkg" "$pkg_url" || die "falha ao baixar $pkg_url"

	if [ -n "$sums_url" ]; then
		curl -fsSL -o "$tmp/checksums.txt" "$sums_url" || die "falha ao baixar checksums.txt"
		expected="$(grep " $(basename "$pkg")\$" "$tmp/checksums.txt" | cut -d' ' -f1)"
		if command -v sha256sum >/dev/null 2>&1; then
			actual="$(sha256sum "$pkg" | cut -d' ' -f1)"
		else
			actual="$(shasum -a 256 "$pkg" | cut -d' ' -f1)"
		fi
		[ -n "$expected" ] && [ "$expected" = "$actual" ] || die "checksum não confere; o download pode estar corrompido"
		say "✓ checksum confere"
	fi
fi

# ---- instalar ----
tar -xzf "$pkg" -C "$tmp"
bin="$(find "$tmp" -type f -name mtsh | head -n1)"
[ -n "$bin" ] || die "o pacote não tem o binário mtsh"

mkdir -p "$INSTALL_DIR"
install -m 0755 "$bin" "$INSTALL_DIR/mtsh"
say "✓ $("$INSTALL_DIR/mtsh" --version) instalado em $INSTALL_DIR/mtsh"

case ":$PATH:" in
	*":$INSTALL_DIR:"*) ;;
	*)
		say ""
		say "! $INSTALL_DIR não está no seu PATH. Adicione no ~/.bashrc ou ~/.zshrc:"
		say "    export PATH=\"$INSTALL_DIR:\$PATH\""
		;;
esac

# ---- ícones das distros ----
answer="${MTSH_ICONS:-}"
if [ -z "$answer" ]; then
	if [ -r /dev/tty ]; then
		printf '\nInstalar os ícones das distros (fonte com os logos)? [S/n] '
		read -r answer </dev/tty || answer=n
	else
		answer=n
	fi
fi
case "$answer" in
	"" | s | S | sim | y | yes)
		say ""
		"$INSTALL_DIR/mtsh" --instalar-icones || say "! não deu pra instalar os ícones agora; dá pra fazer depois nos Ajustes (tecla ,)"
		;;
	*)
		say "  (dá pra instalar depois nos Ajustes do mtsh, tecla ,)"
		;;
esac

say ""
say "Pronto! Rode: mtsh"
