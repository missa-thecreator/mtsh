// Package askpass faz o mtsh funcionar como SSH_ASKPASS.
//
// Quando o mtsh conecta num servidor com senha salva, ele roda o ssh com
// SSH_ASKPASS apontando pro próprio executável. O ssh chama "mtsh <prompt>"
// e lê a resposta do stdout. Se o prompt for de senha, devolvemos a senha
// salva; qualquer outra pergunta (confirmar host novo, passphrase de chave)
// vai pro terminal, pra pessoa responder.
package askpass

import (
	"bufio"
	"fmt"
	"os"
	"strings"

	"golang.org/x/term"
)

const (
	EnvFlag   = "MTSH_ASKPASS"
	EnvSecret = "MTSH_ASKPASS_SECRET"
)

// Active diz se o processo foi chamado pelo ssh como askpass.
func Active() bool { return os.Getenv(EnvFlag) == "1" }

// Env devolve as variáveis pra rodar o ssh usando o mtsh como askpass.
func Env(self, secret string) []string {
	return []string{
		"SSH_ASKPASS=" + self,
		"SSH_ASKPASS_REQUIRE=force",
		EnvFlag + "=1",
		EnvSecret + "=" + secret,
	}
}

// Run responde um prompt do ssh e devolve o código de saída.
func Run(args []string) int {
	prompt := strings.Join(args, " ")
	if strings.Contains(strings.ToLower(prompt), "password") {
		if s := os.Getenv(EnvSecret); s != "" {
			fmt.Println(s)
			return 0
		}
	}
	return askTTY(prompt)
}

func askTTY(prompt string) int {
	tty, err := os.OpenFile("/dev/tty", os.O_RDWR, 0)
	if err != nil {
		return 1
	}
	defer tty.Close()
	fmt.Fprint(tty, prompt)

	// perguntas de sim/não aparecem enquanto a pessoa digita; o resto é secreto
	if strings.Contains(prompt, "(yes/no") {
		line, err := bufio.NewReader(tty).ReadString('\n')
		if err != nil {
			return 1
		}
		fmt.Println(strings.TrimSpace(line))
		return 0
	}
	b, err := term.ReadPassword(int(tty.Fd()))
	fmt.Fprintln(tty)
	if err != nil {
		return 1
	}
	fmt.Println(string(b))
	return 0
}
