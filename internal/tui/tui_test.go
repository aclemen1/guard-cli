package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/aclemen1/tuikit/complete"
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

func arrow(code rune) tea.KeyPressMsg { return tea.KeyPressMsg{Code: code} }

func TestFoldSections(t *testing.T) {
	m := setup(t)
	if it, ok := m.current(); !ok || it.ID != "PG-0001" {
		t.Fatalf("the first rule is selected at start: %+v", it)
	}
	press(m, arrow(tea.KeyLeft))
	if _, ok := m.current(); ok || m.rows[m.sel].item >= 0 {
		t.Fatalf("← on a rule climbs to its section header")
	}
	press(m, arrow(tea.KeyLeft))
	if len(m.rows) != 1 || !strings.Contains(ansi.Strip(m.render()), "▸ Actives (2)") {
		t.Fatalf("← on a header folds its section:\n%s", ansi.Strip(m.render()))
	}
	press(m, arrow(tea.KeyRight))
	if len(m.rows) != 3 || !strings.Contains(ansi.Strip(m.render()), "▾ Actives (2)") {
		t.Fatalf("→ unfolds: %d rows", len(m.rows))
	}
	press(m, key("j"))
	press(m, arrow(tea.KeyRight))
	if !m.cardOn {
		t.Fatal("→ on a rule opens its card")
	}
}

func TestSelect(t *testing.T) {
	m := setup(t)
	press(m, key("j"))
	press(m, key("space"))
	m2 := newModel(m.cfgPath, m.cfg, m.spheres)
	m2.w, m2.h = 120, 30
	m2.wantSel, m2.reveal = "PG-0002", true
	m2.Update(m2.load()())
	if it, ok := m2.current(); !ok || it.ID != "PG-0002" || m2.view != 1 {
		t.Fatalf("--select shows a lifted rule in the view of lifted rules: view %d, %+v", m2.view, it)
	}
	m3 := newModel(m.cfgPath, m.cfg, m.spheres)
	m3.wantSel, m3.reveal = "PG-0042", true
	m3.Update(m3.load()())
	if it, ok := m3.current(); !ok || it.ID != "PG-0001" || m3.view != 0 || !strings.Contains(m3.status, "PG-0042 introuvable") {
		t.Fatalf("an unknown id opens as usual, with a message: %q %+v", m3.status, it)
	}
}

func TestRefCompletion(t *testing.T) {
	refs := filepath.Join(t.TempDir(), "refs.yaml")
	os.WriteFile(refs, []byte("sources:\n  people:\n    prefix: \"contact:\"\n    static:\n      - {value: AGENCY, label: l'agence}\n"), 0o644)
	t.Setenv("TUIKIT_REFS", refs)
	m := setup(t)
	m.cfg.Complete = map[string]complete.Uses{"refs": {{Source: "people"}, {Source: "missing"}}}
	m2 := newModel(m.cfgPath, m.cfg, m.spheres)
	m2.Update(m2.load()())
	if !strings.Contains(m2.status, "missing") {
		t.Fatalf("a missing source is reported: %q", m2.status)
	}
	var got []string
	for _, it := range m2.refCompleter()("") {
		got = append(got, it.Value)
	}
	if strings.Join(got, ",") != "case:deposit,contact:AGENCY" {
		t.Fatalf("cited refs, then the sources: %v", got)
	}
	got = nil
	for _, it := range m2.refCompleter()("agence") {
		got = append(got, it.Value)
	}
	if strings.Join(got, ",") != "contact:AGENCY" {
		t.Fatalf("search by label: %v", got)
	}
}

func TestCompleteFormerKey(t *testing.T) {
	c := &config.Config{Refs: []string{"a"}}
	if got := c.CompleteFor("refs"); len(got) != 1 || got[0].Source != "a" {
		t.Fatalf("former refs key still read: %v", got)
	}
	c.Complete = map[string]complete.Uses{"refs": {{Source: "b"}}}
	if got := c.CompleteFor("refs"); len(got) != 1 || got[0].Source != "b" {
		t.Fatalf("complete.refs wins: %v", got)
	}
}

func TestCardJournal(t *testing.T) {
	m := setup(t)
	m.cfg.History.Ls = []string{"echo", "entrée du journal de {id}"}
	_, cmd := m.Update(m.load()())
	if cmd == nil {
		t.Fatal("a load asks for the journal of the rule shown")
	}
	m.Update(cmd())
	if v := ansi.Strip(m.render()); !strings.Contains(v, "Journal") || !strings.Contains(v, "entrée du journal de PG-0001") {
		t.Fatalf("the card shows the journal:\n%s", v)
	}
}
