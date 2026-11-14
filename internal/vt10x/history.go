package vt10x

// Histórico de rolagem (scrollback). Não existe no vt10x original: foi
// acrescentado pelo mtsh. As linhas que saem pelo topo da tela principal
// (nunca da tela alternativa do vim, htop, less…) são guardadas aqui.

// HistoryMax é quantas linhas o histórico guarda; as mais antigas saem.
const HistoryMax = 10000

// pushHistory guarda uma cópia das linhas que vão sair pelo topo.
func (t *State) pushHistory(ls []line) {
	t.pushed += len(ls)
	for _, l := range ls {
		t.history = append(t.history, append(line(nil), l...))
	}
	if over := len(t.history) - HistoryMax; over > 0 {
		t.history = append([]line(nil), t.history[over:]...)
	}
}

// HistoryLen é quantas linhas há no histórico.
func (t *State) HistoryLen() int { return len(t.history) }

// HistoryLine devolve a linha i do histórico (0 = a mais antiga).
func (t *State) HistoryLine(i int) []Glyph {
	if i < 0 || i >= len(t.history) {
		return nil
	}
	return t.history[i]
}

// HistoryPushed conta todas as linhas que já entraram no histórico, mesmo
// as que já saíram pelo limite. Serve pra manter parado o trecho que a pessoa
// está lendo enquanto chega saída nova.
func (t *State) HistoryPushed() int { return t.pushed }

// ClearHistory apaga o histórico (como o "clear" faz nos terminais).
func (t *State) ClearHistory() { t.history = nil }
