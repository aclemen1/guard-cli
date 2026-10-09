package tui

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
	"time"

	"charm.land/lipgloss/v2"
)

const stateEnv = "GUARD_TUI_STATE"

func lipStyle(c adaptive) lipgloss.Style { return lipgloss.NewStyle().Foreground(c) }

// stamp identifies the binary on disk: a new install changes it.
func stamp(path string) string {
	fi, err := os.Stat(path)
	if err != nil || fi.Size() == 0 {
		return ""
	}
	return fmt.Sprintf("%s/%d", fi.ModTime().Format(time.RFC3339Nano), fi.Size())
}

// checkBin asks for a restart once a new binary has stayed the same for two
// checks, and only when nothing is being typed.
func (m *model) checkBin() bool {
	if m.exe == "" {
		return false
	}
	now := stamp(m.exe)
	switch {
	case now == "" || now == m.exeStamp:
		m.newStamp = ""
		return false
	case now != m.newStamp:
		m.newStamp = now
		return false
	}
	if m.filtering || m.modal.Open() {
		m.say("nouvelle version installée : relance dès la fin de la saisie", false)
		return false
	}
	return true
}

// saved is what a restart keeps.
type saved struct {
	View   int      `json:"view"`
	Filter string   `json:"filter,omitempty"`
	Only   string   `json:"only,omitempty"`
	Sel    string   `json:"sel,omitempty"`
	Detail bool     `json:"detail"`
	Folded []string `json:"folded,omitempty"`
}

func (m *model) restore() {
	raw := os.Getenv(stateEnv)
	if raw == "" {
		return
	}
	os.Unsetenv(stateEnv)
	var s saved
	if json.Unmarshal([]byte(raw), &s) != nil {
		return
	}
	if s.View >= 0 && s.View < len(views) {
		m.view = s.View
	}
	m.filter, m.only, m.detailOn = s.Filter, s.Only, s.Detail
	m.wantSel = s.Sel
	for _, g := range s.Folded {
		if m.folded == nil {
			m.folded = map[string]bool{}
		}
		m.folded[g] = true
	}
	m.say("nouvelle version chargée", false)
}

// reexec replaces the process with the new binary, on the same view and selection.
func (m *model) reexec() error {
	s := saved{View: m.view, Filter: m.filter, Only: m.only, Detail: m.detailOn}
	for g, on := range m.folded {
		if on {
			s.Folded = append(s.Folded, g)
		}
	}
	if it, ok := m.current(); ok {
		s.Sel = it.ID
	}
	b, _ := json.Marshal(s)
	env := append(os.Environ(), stateEnv+"="+string(b))
	return syscall.Exec(m.exe, os.Args, env)
}

func executable() string {
	p, err := os.Executable()
	if err != nil {
		return ""
	}
	if r, err := filepath.EvalSymlinks(p); err == nil {
		return r
	}
	return p
}
