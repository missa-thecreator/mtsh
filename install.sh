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
#   MTSH_PATH=sim|nao          colocar a pasta no PATH sem perguntar
#   MTSH_REPO=dono/repo        repositório de onde baixar (padrão: missa-thecreator/mtsh)
set -eu

REPO="${MTSH_REPO:-missa-thecreator/mtsh}"
API="${MTSH_GITHUB_API:-https://api.github.com}"
INSTALL_DIR="${MTSH_INSTALL_DIR:-$HOME/.local/bin}"

say() { printf '%s\n' "$*"; }

# ask PERGUNTA VALOR_PRONTO: sim/não pelo terminal (funciona mesmo com curl | sh).
# Sem terminal e sem valor pronto, a resposta é "não".
ask() {
	a="$2"
	if [ -z "$a" ]; then
		if [ -r /dev/tty ]; then
			printf '\n%s [S/n] ' "$1"
			read -r a </dev/tty || a=n
		else
			a=n
		fi
	fi
	case "$a" in "" | s | S | sim | y | yes) return 0 ;; *) return 1 ;; esac
}
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

# ---- PATH ----
case ":$PATH:" in
	*":$INSTALL_DIR:"*) ;;
	*)
		case "${SHELL:-}" in
			*/zsh) rc="$HOME/.zshrc" ;;
			*/bash) rc="$HOME/.bashrc" ;;
			*) rc="$HOME/.profile" ;;
		esac
		# grava $HOME literal quando dá, pra a linha valer em qualquer máquina
		dir_expr="$INSTALL_DIR"
		case "$INSTALL_DIR" in "$HOME"/*) dir_expr="\$HOME${INSTALL_DIR#"$HOME"}" ;; esac
		line="case \":\$PATH:\" in *\":$dir_expr:\"*) ;; *) export PATH=\"$dir_expr:\$PATH\" ;; esac"

		say ""
		say "! $INSTALL_DIR ainda não está no seu PATH."
		if [ -f "$rc" ] && grep -qF "# mtsh: PATH" "$rc"; then
			say "  Já está configurado no $rc; vale nos próximos terminais."
		elif ask "Adicionar ao PATH no $rc?" "${MTSH_PATH:-}"; then
			printf '\n# mtsh: PATH\n%s\n' "$line" >>"$rc"
			say "✓ adicionado ao $rc; vale nos próximos terminais"
		else
			say "  Pra adicionar depois, coloque no $rc:"
			say "    export PATH=\"$dir_expr:\$PATH\""
		fi
		say "  Pra usar já neste terminal:  export PATH=\"$INSTALL_DIR:\$PATH\""
		;;
esac

# ---- ícones das distros ----
if ask "Instalar os ícones das distros (fonte com os logos)?" "${MTSH_ICONS:-}"; then
	say ""
	"$INSTALL_DIR/mtsh" --instalar-icones || say "! não deu pra instalar os ícones agora; dá pra fazer depois nos Ajustes (tecla ,)"
else
	say "  (dá pra instalar depois nos Ajustes do mtsh, tecla ,)"
fi

say ""
case ":$PATH:" in
	*":$INSTALL_DIR:"*) say "Pronto! Rode: mtsh" ;;
	*) say "Pronto! Abra um novo terminal e rode: mtsh  (ou, agora mesmo: $INSTALL_DIR/mtsh)" ;;
esac
