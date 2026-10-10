package tui

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

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
	drive(m, cmd)
}

// drive runs cmd and feeds its messages back to m, batches included; the
// Busy ticks and what does not answer within a second are dropped.
func drive(m *model, cmd tea.Cmd) {
	for _, msg := range collect(cmd) {
		_, next := m.Update(msg)
		drive(m, next)
	}
}

func collect(cmd tea.Cmd) []tea.Msg {
	if cmd == nil {
		return nil
	}
	ch := make(chan tea.Msg, 1)
	go func() { ch <- cmd() }()
	var msg tea.Msg
	select {
	case msg = <-ch:
	case <-time.After(time.Second):
		return nil
	}
	if batch, ok := msg.(tea.BatchMsg); ok {
		var out []tea.Msg
		for _, c := range batch {
			out = append(out, collect(c)...)
		}
		return out
	}
	if msg == nil || fmt.Sprintf("%T", msg) == "tuikit.busyTickMsg" {
		return nil
	}
	return []tea.Msg{msg}
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
	if v := ansi.Strip(m.render()); !strings.Contains(v, "journal de PG-0001") {
		t.Fatalf("the first read of a journal shows in the header:\n%s", v)
	}
	drive(m, cmd)
	if v := ansi.Strip(m.render()); !strings.Contains(v, "Journal") || !strings.Contains(v, "entrée du journal de PG-0001") {
		t.Fatalf("the card shows the journal:\n%s", v)
	}
	_, cmd = m.Update(m.load()())
	if cmd == nil || m.busy.Running() != 0 {
		t.Fatal("a journal already read is read again quietly after a load")
	}
}

func TestBusy(t *testing.T) {
	m := setup(t)
	setOpen := func(script string) {
		raw, _ := os.ReadFile(m.cfgPath)
		raw = append(raw, []byte("\nopen:\n  run: [sh, -c, '"+script+"']\n")...)
		os.WriteFile(m.cfgPath, raw, 0o644)
	}
	setOpen("sleep 0.2; echo ouvert")
	_, cmd := m.Update(key("o"))
	if v := ansi.Strip(m.render()); !strings.Contains(v, "ouvrir PG-0001") {
		t.Fatalf("a job under way shows in the header:\n%s", v)
	}
	drive(m, cmd)
	if v := ansi.Strip(m.render()); !strings.Contains(v, "✓ ouvert") {
		t.Fatalf("the end of the job shows its text:\n%s", v)
	}
	raw, _ := os.ReadFile(m.cfgPath)
	os.WriteFile(m.cfgPath, []byte(strings.Split(string(raw), "\nopen:")[0]), 0o644)
	setOpen("echo refusé >&2; exit 3")
	press(m, key("o"))
	press(m, key("j"))
	v := ansi.Strip(m.render())
	if !strings.Contains(v, "✗ ouvrir PG-0001") || !strings.Contains(v, "! voir l'échec") {
		t.Fatalf("a failure stays in sight, with ! in the footer:\n%s", v)
	}
	press(m, key("!"))
	if !m.modal.Open() || !strings.Contains(ansi.Strip(m.render()), "Travaux") {
		t.Fatalf("! opens the list of jobs:\n%s", ansi.Strip(m.render()))
	}
	if m.busy.Unread() != 0 {
		t.Fatal("opening the list reads the failure")
	}
	press(m, key("!"))
	if m.modal.Open() {
		t.Fatal("! closes the list")
	}
	press(m, key("space"))
	if v := ansi.Strip(m.render()); !strings.Contains(v, "✓ PG-0002 levée") {
		t.Fatalf("an action ends with its text:\n%s", v)
	}
	press(m, key("c"))
	press(m, key("!"))
	if !m.modal.Open() || strings.Contains(ansi.Strip(m.render()), "Travaux") {
		t.Fatal("! in a form is typed, not a key of the TUI")
	}
}
