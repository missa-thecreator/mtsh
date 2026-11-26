package importer

import (
	"strings"
	"testing"

	"mtsh/internal/config"
)

// Arquivo no formato real: CRLF, Windows-1252 ("Produção" em bytes Latin-1),
// sessão RDP misturada, códigos especiais e _CurrentDrive_ no caminho da chave.
var mobaSample = strings.ReplaceAll(
	"[Bookmarks]\n"+
		"SubRep=\n"+
		"ImgNum=42\n"+
		"nas= #109#0%192.168.1.20%22%admin%%-1%-1%%%22%%0%0%0%%%-1%0%0%0%%1080%%0%0%1#MobaFont%10%0%0%-1%15#0# #-1\n"+
		"\n"+
		"[Bookmarks_1]\n"+
		"SubRep=Trabalho\\Produ\xe7\xe3o\n"+
		"ImgNum=41\n"+
		"web-01= #109#0%web-01.example.com%2222%deploy%%-1%-1%%%22%%0%0%0%_CurrentDrive_:\\Users\\eu\\.ssh\\id_ed25519%%-1%0%0%0%%1080%%0%0%1#MobaFont%10#0# #-1\n"+
		"cliente__PIPE__a= #109#0%10.1.1.1%22%root%%-1%-1%%%22%%0%0%0%%%-1%0%0%0%%1080%%0%0%1#MobaFont%10#0# #-1\n"+
		"desktop-rdp= #91#4%10.1.1.2%3389%%-1%0%0%0%%%%%0%0%%%0#MobaFont%10#0# #-1\n"+
		"web-01= #109#0%web-01b.example.com%22%deploy%%-1%-1%%%22%%0%0%0%%%-1%0%0%0%%1080%%0%0%1#MobaFont%10#0# #-1\n"+
		"\n"+
		"[Passwords]\n"+
		"secreto=naoleia\n",
	"\n", "\r\n")

func TestMobaXterm(t *testing.T) {
	items, skipped, err := MobaXterm([]byte(mobaSample))
	if err != nil {
		t.Fatal(err)
	}
	if skipped != 1 {
		t.Errorf("deveria pular só a sessão RDP, pulou %d", skipped)
	}
	if len(items) != 4 {
		t.Fatalf("quero 4 servidores SSH, veio %d: %+v", len(items), items)
	}
	byName := map[string]Item{}
	for _, it := range items {
		byName[it.Host.Name] = it
	}

	nas := byName["nas"]
	if nas.Host.Folder != "" || nas.Host.Host != "192.168.1.20" || nas.Host.User != "admin" || nas.Host.Port != 0 {
		t.Errorf("nas veio errado: %+v", nas.Host)
	}
	if nas.Host.Auth != config.AuthAuto || nas.KeyPath != "" {
		t.Errorf("sem chave e sem senha, nas deveria ser automático: %+v", nas)
	}

	web := byName["web-01"]
	if web.Host.Folder != "Trabalho/Produção" {
		t.Errorf("pasta = %q (Windows-1252 e subpasta com \\)", web.Host.Folder)
	}
	if web.Host.Port != 2222 || web.Host.User != "deploy" {
		t.Errorf("porta/usuário de web-01: %+v", web.Host)
	}
	if web.KeyPath != `C:\Users\eu\.ssh\id_ed25519` {
		t.Errorf("caminho da chave = %q", web.KeyPath)
	}

	if _, ok := byName["cliente|a"]; !ok {
		t.Errorf("__PIPE__ no nome deveria virar |: %v", byName)
	}
	if dup, ok := byName["web-01 (2)"]; !ok || dup.Host.Host != "web-01b.example.com" {
		t.Errorf("nome repetido deveria virar \"web-01 (2)\": %v", byName)
	}
	for _, it := range items {
		if it.Password != "" {
			t.Errorf("%s não deveria trazer senha (a seção [Passwords] é criptografada)", it.Host.Name)
		}
	}
}

func TestMobaXtermArquivoErrado(t *testing.T) {
	if _, _, err := MobaXterm([]byte("[Geral]\nfoo=bar\n")); err == nil {
		t.Error("arquivo sem [Bookmarks] deveria dar erro")
	}
}

func TestDetect(t *testing.T) {
	if _, _, src, err := Detect([]byte(mobaSample)); err != nil || src != "MobaXterm" {
		t.Errorf("MobaXterm: %q %v", src, err)
	}
	if _, _, src, err := Detect([]byte(`{"bookmarks": []}`)); err != nil || src != "electerm" {
		t.Errorf("electerm: %q %v", src, err)
	}
	if _, _, _, err := Detect([]byte("qualquer coisa")); err == nil {
		t.Error("arquivo desconhecido deveria dar erro")
	}
}
