package ui

import (
	tea "github.com/charmbracelet/bubbletea"
)

// encodeKey traduz uma tecla do Bubble Tea pros bytes que um terminal
// mandaria pro servidor.
func encodeKey(k tea.KeyMsg, appCursor bool) []byte {
	var out []byte
	switch k.Type {
	case tea.KeyRunes:
		out = []byte(string(k.Runes))
	case tea.KeySpace:
		out = []byte{' '}

	case tea.KeyUp, tea.KeyDown, tea.KeyRight, tea.KeyLeft:
		dir := map[tea.KeyType]byte{tea.KeyUp: 'A', tea.KeyDown: 'B', tea.KeyRight: 'C', tea.KeyLeft: 'D'}[k.Type]
		if appCursor {
			out = []byte{0x1b, 'O', dir}
		} else {
			out = []byte{0x1b, '[', dir}
		}
	case tea.KeyShiftUp:
		out = []byte("\x1b[1;2A")
	case tea.KeyShiftDown:
		out = []byte("\x1b[1;2B")
	case tea.KeyShiftRight:
		out = []byte("\x1b[1;2C")
	case tea.KeyShiftLeft:
		out = []byte("\x1b[1;2D")
	case tea.KeyCtrlUp:
		out = []byte("\x1b[1;5A")
	case tea.KeyCtrlDown:
		out = []byte("\x1b[1;5B")
	case tea.KeyCtrlRight:
		out = []byte("\x1b[1;5C")
	case tea.KeyCtrlLeft:
		out = []byte("\x1b[1;5D")

	case tea.KeyHome:
		out = []byte("\x1b[H")
	case tea.KeyEnd:
		out = []byte("\x1b[F")
	case tea.KeyPgUp:
		out = []byte("\x1b[5~")
	case tea.KeyPgDown:
		out = []byte("\x1b[6~")
	case tea.KeyInsert:
		out = []byte("\x1b[2~")
	case tea.KeyDelete:
		out = []byte("\x1b[3~")
	case tea.KeyShiftTab:
		out = []byte("\x1b[Z")

	case tea.KeyF1:
		out = []byte("\x1bOP")
	case tea.KeyF2:
		out = []byte("\x1bOQ")
	case tea.KeyF3:
		out = []byte("\x1bOR")
	case tea.KeyF4:
		out = []byte("\x1bOS")
	case tea.KeyF5:
		out = []byte("\x1b[15~")
	case tea.KeyF6:
		out = []byte("\x1b[17~")
	case tea.KeyF7:
		out = []byte("\x1b[18~")
	case tea.KeyF8:
		out = []byte("\x1b[19~")
	case tea.KeyF9:
		out = []byte("\x1b[20~")
	case tea.KeyF10:
		out = []byte("\x1b[21~")
	case tea.KeyF11:
		out = []byte("\x1b[23~")
	case tea.KeyF12:
		out = []byte("\x1b[24~")

	default:
		// enter, tab, esc, backspace e ctrl+letra: no Bubble Tea v1 o tipo
		// da tecla é o próprio código de controle (0–31 e 127)
		if (k.Type >= 0 && k.Type < 32) || k.Type == 127 {
			out = []byte{byte(k.Type)}
		}
	}
	if k.Alt && len(out) > 0 {
		out = append([]byte{0x1b}, out...)
	}
	return out
}
