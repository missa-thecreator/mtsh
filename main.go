package main

import (
	"flag"
	"fmt"
	"io"
	"log"
	"os"

	tea "github.com/charmbracelet/bubbletea"

	"mtsh/internal/askpass"
	"mtsh/internal/config"
	"mtsh/internal/fonts"
	"mtsh/internal/ui"
	"mtsh/internal/vault"
)

// version é definida no build (scripts/build.sh usa -ldflags "-X main.version=...")
var version = "dev"

func main() {
	// chamado pelo ssh pra responder a senha (ver internal/askpass)
	if askpass.Active() {
		os.Exit(askpass.Run(os.Args[1:]))
	}

	// MTSH_DEBUG=arquivo.log grava eventos (mouse etc.) pra diagnóstico
	if path := os.Getenv("MTSH_DEBUG"); path != "" {
		f, err := tea.LogToFile(path, "mtsh ")
		if err != nil {
			fail(err)
		}
		defer f.Close()
	} else {
		log.SetOutput(io.Discard)
	}

	cfgPath := flag.String("config", config.DefaultPath(), "arquivo de servidores (yaml)")
	installIcons := flag.Bool("instalar-icones", false, "instala a fonte com os logos das distros e configura o terminal")
	showVersion := flag.Bool("version", false, "mostra a versão")
	flag.Parse()

	if *showVersion {
		fmt.Println("mtsh", version)
		return
	}

	if *installIcons {
		if err := fonts.Install(os.Stdout); err != nil {
			fail(err)
		}
		return
	}

	cfg, err := config.Load(*cfgPath)
	if err != nil {
		fail(err)
	}
	v, err := vault.Open(config.PasswordsPath(*cfgPath))
	if err != nil {
		fail(err)
	}

	p := tea.NewProgram(ui.New(*cfgPath, cfg, v), tea.WithAltScreen(), tea.WithMouseCellMotion())
	if _, err := p.Run(); err != nil {
		fail(err)
	}
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, "mtsh:", err)
	os.Exit(1)
}
