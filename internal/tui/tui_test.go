package tui

import (
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/aclemen1/guard-cli/internal/config"
	"github.com/aclemen1/guard-cli/internal/spec"
	"github.com/aclemen1/guard-cli/internal/store"
)

func setup(t *testing.T) *model {
	t.Helper()
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.yaml")
	cfg, _ := config.Load(cfgPath)
	root := filepath.Join(dir, "perso")
	if err := store.Init(root, "none"); err != nil {
		t.Fatal(err)
	}
	if err := cfg.AddSphere("perso", config.Sphere{Root: root, Prefix: "P", VCS: "none"}); err != nil {
		t.Fatal(err)
	}
	cfg, _ = config.Load(cfgPath)
	add := spec.Find("guard", "add")
	for _, a := range []map[string]any{
		{"title": "Never sign the debtor's commitment", "sphere": "perso", "ref": []string{"case:deposit"}, "when": "when the agency sends a paper"},
		{"title": "Nothing without the insurer", "sphere": "perso", "ref": []string{"case:deposit"}},
	} {
		if _, err := add.Run(&spec.Context{Args: a, Config: cfgPath}); err != nil {
			t.Fatal(err)
		}
	}
	m := newModel(cfgPath, cfg, []string{"perso"})
	m.w, m.h = 120, 30
	m.Update(m.load()())
	return m
}

// press sends a key and runs the command it returns, as the program would.
func press(m *model, k tea.KeyPressMsg) {
	_, cmd := m.Update(k)
	for cmd != nil {
		msg := cmd()
		if msg == nil {
			return
		}
		_, cmd = m.Update(msg)
	}
}

func key(s string) tea.KeyPressMsg {
	if s == "space" {
		return tea.KeyPressMsg{Code: tea.KeySpace, Text: " "}
	}
	return tea.KeyPressMsg{Code: rune(s[0]), Text: s}
}

func TestListAndLift(t *testing.T) {
	m := setup(t)
	view := ansi.Strip(m.render())
	for _, want := range []string{"2 consignes actives", "Actives", "PG-0001", "Never sign", "when the agency sends a paper", "case:deposit"} {
		if !strings.Contains(view, want) {
			t.Errorf("view lacks %q:\n%s", want, view)
		}
	}
	press(m, key("space"))
	if len(m.items) != 1 || m.items[0].ID != "PG-0002" {
		t.Fatalf("lifted rule leaves the active view: %+v", m.items)
	}
	press(m, key("2"))
	if len(m.items) != 1 || m.items[0].ID != "PG-0001" || m.items[0].State != store.Lifted {
		t.Fatalf("lifted view: %+v", m.items)
	}
	press(m, key("space"))
	press(m, key("3"))
	if len(m.items) != 2 {
		t.Fatalf("restored: %+v", m.items)
	}
}

func TestModalTakesKeys(t *testing.T) {
	m := setup(t)
	press(m, key("c"))
	if !m.modal.Open() {
		t.Fatal("c opens the form")
	}
	press(m, key("q"))
	if !m.modal.Open() {
		t.Fatal("q goes to the form, not to the TUI")
	}
}
