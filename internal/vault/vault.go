// Package vault guarda as senhas dos servidores.
//
// As senhas ficam num JSON com permissão 600, legível só pelo usuário,
// o mesmo nível de proteção das chaves em ~/.ssh e das chaves coladas
// no mtsh. Não tem criptografia: sem uma senha mestra ela seria só
// aparência, já que a chave teria que ficar no mesmo computador.
package vault

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

type Vault struct {
	path    string
	entries map[string]string
}

// Open lê o arquivo de senhas (ou começa vazio se ele não existir).
func Open(path string) (*Vault, error) {
	v := &Vault{path: path, entries: map[string]string{}}
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return v, nil
	}
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(data, &v.entries); err != nil {
		return nil, fmt.Errorf("arquivo de senhas inválido (%s): %w", path, err)
	}
	if v.entries == nil {
		v.entries = map[string]string{}
	}
	return v, nil
}

func (v *Vault) Has(name string) bool { _, ok := v.entries[name]; return ok }

func (v *Vault) Get(name string) (string, error) {
	p, ok := v.entries[name]
	if !ok {
		return "", fmt.Errorf("sem senha salva para %s", name)
	}
	return p, nil
}

func (v *Vault) Set(name, secret string) error {
	v.entries[name] = secret
	return v.save()
}

func (v *Vault) Delete(name string) error {
	if !v.Has(name) {
		return nil
	}
	delete(v.entries, name)
	return v.save()
}

// Rename move a senha de um nome de servidor para outro.
func (v *Vault) Rename(from, to string) error {
	p, ok := v.entries[from]
	if !ok || from == to {
		return nil
	}
	delete(v.entries, from)
	v.entries[to] = p
	return v.save()
}

func (v *Vault) save() error {
	if err := os.MkdirAll(filepath.Dir(v.path), 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(v.entries, "", "  ")
	if err != nil {
		return err
	}
	tmp := v.path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, v.path)
}
