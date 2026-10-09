package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/aclemen1/guard-cli/internal/actions"
)

type env struct {
	t   *testing.T
	dir string
	cfg string
}

func setup(t *testing.T) *env {
	t.Helper()
	dir := t.TempDir()
	cfg := filepath.Join(dir, "config.yaml")
	t.Setenv("GUARD_SPHERE", "")
	t.Setenv("GUARD_BY", "")
	loc, _ := time.LoadLocation("Europe/Zurich")
	now := time.Date(2026, 10, 9, 18, 30, 0, 0, loc)
	actions.SetClock(func() time.Time { return now })
	t.Cleanup(func() { actions.SetClock(nil) })
	e := &env{t, dir, cfg}
	e.ok("init", "--sphere", "perso", "--root", filepath.Join(dir, "perso"), "--vcs", "none")
	e.ok("init", "--sphere", "pro", "--root", filepath.Join(dir, "pro"), "--vcs", "none", "--prefix", "U")
	return e
}

// appendConfig adds YAML to the configuration file.
func (e *env) appendConfig(s string) {
	f, err := os.OpenFile(e.cfg, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		e.t.Fatal(err)
	}
	f.WriteString(s)
	f.Close()
}

// hook records each call of a hook in a file: event, id, key.
func (e *env) hook() string {
	log := filepath.Join(e.dir, "hook.log")
	script := filepath.Join(e.dir, "hook.sh")
	os.WriteFile(script, []byte("#!/bin/sh\ncat > /dev/null\necho \"$GUARD_EVENT $GUARD_ID${GUARD_KEY:+ $GUARD_KEY}\" >> "+log+"\n"), 0o755)
	e.appendConfig("hooks:\n  - run: [\"" + script + "\"]\n")
	return log
}

func (e *env) call(args ...string) (int, map[string]any) {
	e.t.Helper()
	var out, errw bytes.Buffer
	code := run(append(args, "--config", e.cfg), strings.NewReader(""), &out, &errw)
	var m map[string]any
	if err := json.Unmarshal(out.Bytes(), &m); err != nil {
		e.t.Fatalf("%v: not JSON: %s %s", args, out.String(), errw.String())
	}
	return code, m
}

func (e *env) ok(args ...string) map[string]any {
	e.t.Helper()
	code, m := e.call(args...)
	if code != 0 {
		e.t.Fatalf("%v: exit %d: %v", args, code, m["error"])
	}
	r, _ := m["result"].(map[string]any)
	return r
}

func (e *env) list(args ...string) []map[string]any {
	e.t.Helper()
	code, m := e.call(args...)
	if code != 0 {
		e.t.Fatalf("%v: exit %d: %v", args, code, m["error"])
	}
	var out []map[string]any
	for _, x := range m["result"].([]any) {
		out = append(out, x.(map[string]any))
	}
	return out
}

func (e *env) fails(code int, args ...string) string {
	e.t.Helper()
	got, m := e.call(args...)
	if got != code {
		e.t.Fatalf("%v: exit %d, want %d: %v", args, got, code, m)
	}
	return m["error"].(map[string]any)["message"].(string)
}

func ids(items []map[string]any) string {
	var out []string
	for _, it := range items {
		out = append(out, it["id"].(string))
	}
	return strings.Join(out, ",")
}

func TestAddShowAndStore(t *testing.T) {
	e := setup(t)
	r := e.ok("add", "Never sign the debtor's commitment", "--sphere", "perso",
		"--ref", "case:deposit", "--ref", "contact:AGENCY", "--trigger", "with:contact:AGENCY",
		"--when", "when the agency sends a paper to sign", "--source", "case:deposit#1", "--body", "Only a written settlement binds.")
	if r["id"] != "PG-0001" || r["state"] != "active" || r["sphere"] != "perso" || r["trigger"] != "with:contact:AGENCY" {
		t.Fatalf("%v", r)
	}
	b, err := os.ReadFile(filepath.Join(e.dir, "perso", "PG-0001.md"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"title: Never sign the debtor's commitment", "- case:deposit", "trigger: with:contact:AGENCY", "Only a written settlement binds."} {
		if !strings.Contains(string(b), want) {
			t.Errorf("file lacks %q:\n%s", want, b)
		}
	}
	if r := e.ok("add", "Keep the minutes", "--sphere", "pro"); r["id"] != "UG-0001" {
		t.Fatalf("pro prefix: %v", r)
	}
	if r := e.ok("show", "PG-0001"); r["when"] != "when the agency sends a paper to sign" {
		t.Fatalf("%v", r)
	}
	if r := e.ok("show", "1", "--sphere", "pro"); r["id"] != "UG-0001" {
		t.Fatalf("%v", r)
	}
	e.fails(2, "show", "1")
	e.fails(3, "show", "PG-0042")
}

func TestAddChecks(t *testing.T) {
	e := setup(t)
	if msg := e.fails(2, "add", "No sphere"); !strings.Contains(msg, "--sphere") {
		t.Errorf("%s", msg)
	}
	if msg := e.fails(2, "add", "Dated", "--sphere", "perso", "--trigger", "at:2026-10-15"); !strings.Contains(msg, "event") {
		t.Errorf("at: is no trigger of a rule: %s", msg)
	}
	e.fails(2, "add", "Bad", "--sphere", "perso", "--trigger", "on:mail")
	e.fails(2, "add", "Bad ref", "--sphere", "perso", "--ref", "deposit")
	e.fails(2, "add", "Bad source", "--sphere", "perso", "--source", "nope")
}

func TestSourceAndUpsert(t *testing.T) {
	e := setup(t)
	log := e.hook()
	e.ok("add", "Nothing without the insurer", "--sphere", "perso", "--source", "case:deposit#2")
	e.fails(4, "add", "Nothing without the insurer", "--sphere", "perso", "--source", "case:deposit#2")
	r := e.ok("add", "Nothing agreed without the insurer", "--sphere", "perso", "--source", "case:deposit#2", "--upsert", "--when", "before any agreement")
	if r["id"] != "PG-0001" || r["title"] != "Nothing agreed without the insurer" || r["when"] != "before any agreement" {
		t.Fatalf("%v", r)
	}
	e.ok("add", "Nothing agreed without the insurer", "--sphere", "perso", "--source", "case:deposit#2", "--upsert")
	b, _ := os.ReadFile(log)
	if got := strings.TrimSpace(string(b)); got != "added PG-0001\nedited PG-0001" {
		t.Fatalf("hooks: %q", got)
	}
}

func TestListFilters(t *testing.T) {
	e := setup(t)
	e.ok("add", "A", "--sphere", "perso", "--ref", "case:deposit", "--trigger", "with:contact:AGENCY")
	e.ok("add", "B", "--sphere", "perso", "--ref", "case:deposit,contact:VP", "--trigger", "on:mail:reply:thread/abc")
	e.ok("add", "C", "--sphere", "perso", "--ref", "case:other", "--when", "if a claim is considered")
	e.ok("add", "D", "--sphere", "pro", "--ref", "case:deposit", "--trigger", "with:contact:JMR")
	if got := ids(e.list("ls", "--ref", "case:deposit")); got != "PG-0001,PG-0002,UG-0001" {
		t.Errorf("--ref: %s", got)
	}
	if got := ids(e.list("ls", "--ref", "case:deposit", "--sphere", "pro")); got != "UG-0001" {
		t.Errorf("--sphere: %s", got)
	}
	if got := ids(e.list("ls", "--trigger", "with:")); got != "PG-0001,UG-0001" {
		t.Errorf("--trigger with: %s", got)
	}
	if got := ids(e.list("ls", "--trigger", "with:contact:AGENCY")); got != "PG-0001" {
		t.Errorf("--trigger contact: %s", got)
	}
	if got := ids(e.list("ls", "--trigger", "with:cont")); got != "" {
		t.Errorf("--trigger matches whole segments: %s", got)
	}
	if got := ids(e.list("ls", "--search", "claim")); got != "PG-0003" {
		t.Errorf("--search: %s", got)
	}
	e.ok("lift", "PG-0001")
	if got := ids(e.list("ls", "--ref", "case:deposit", "--sphere", "perso")); got != "PG-0002" {
		t.Errorf("lifted leaves ls: %s", got)
	}
	if got := ids(e.list("ls", "--state", "lifted")); got != "PG-0001" {
		t.Errorf("--state lifted: %s", got)
	}
	if got := ids(e.list("ls", "--state", "all", "--sphere", "perso")); got != "PG-0001,PG-0002,PG-0003" {
		t.Errorf("--state all: %s", got)
	}
}

func TestFire(t *testing.T) {
	e := setup(t)
	log := e.hook()
	e.ok("add", "Reply only after the lawyer", "--sphere", "perso", "--trigger", "on:mail:reply")
	e.ok("add", "This thread only", "--sphere", "perso", "--trigger", "on:mail:reply:thread/abc")
	e.ok("add", "Other thread", "--sphere", "perso", "--trigger", "on:mail:reply:thread/xyz")
	e.ok("add", "Prefix of a word", "--sphere", "perso", "--trigger", "on:mail:rep")
	e.ok("add", "Lifted", "--sphere", "pro", "--trigger", "on:mail:reply")
	e.ok("lift", "UG-0001")
	os.Remove(log)
	if got := ids(e.list("fire", "on:mail:reply:thread/abc", "--dry-run")); got != "PG-0001,PG-0002" {
		t.Fatalf("matches: %s", got)
	}
	if _, err := os.Stat(log); err == nil {
		t.Fatalf("--dry-run ran hooks")
	}
	e.list("fire", "on:mail:reply:thread/abc")
	b, _ := os.ReadFile(log)
	want := "fired PG-0001 on:mail:reply:thread/abc\nfired PG-0002 on:mail:reply:thread/abc"
	if got := strings.TrimSpace(string(b)); got != want {
		t.Fatalf("hooks: %q", got)
	}
	if got := ids(e.list("fire", "with:contact:JMR")); got != "" {
		t.Fatalf("no match: %s", got)
	}
	e.fails(2, "fire", "nothing")
}

func TestLiftRestoreEdit(t *testing.T) {
	e := setup(t)
	log := e.hook()
	e.ok("add", "Rule", "--sphere", "perso", "--ref", "case:a", "--trigger", "with:contact:X")
	if r := e.ok("lift", "PG-0001", "--note", "settled"); r["state"] != "lifted" || r["lifted"] == nil {
		t.Fatalf("%v", r)
	}
	e.ok("lift", "PG-0001")
	e.ok("restore", "PG-0001")
	e.ok("restore", "PG-0001")
	r := e.ok("edit", "PG-0001", "--add-ref", "contact:X", "--remove-ref", "case:a", "--trigger", "none", "--when", "when X calls")
	refs, _ := r["refs"].([]any)
	if len(refs) != 1 || refs[0] != "contact:X" || r["trigger"] != nil || r["when"] != "when X calls" {
		t.Fatalf("%v", r)
	}
	e.ok("edit", "PG-0001", "--when", "when X calls")
	r = e.ok("edit", "PG-0001", "--ref", "none")
	if r["refs"] != nil {
		t.Fatalf("--ref none: %v", r)
	}
	b, _ := os.ReadFile(log)
	want := "added PG-0001\nlifted PG-0001\nrestored PG-0001\nedited PG-0001\nedited PG-0001"
	if got := strings.TrimSpace(string(b)); got != want {
		t.Fatalf("an action that changes nothing fires nothing: %q", got)
	}
	r = e.ok("show", "PG-0001")
	log2, _ := json.Marshal(r["log"])
	if !strings.Contains(string(log2), "lifted: settled") {
		t.Fatalf("history: %s", log2)
	}
}

func TestRemove(t *testing.T) {
	e := setup(t)
	e.ok("add", "Typo", "--sphere", "perso")
	e.ok("rm", "PG-0001")
	if _, err := os.Stat(filepath.Join(e.dir, "perso", "PG-0001.md")); !os.IsNotExist(err) {
		t.Fatalf("file still there")
	}
	e.fails(3, "rm", "PG-0001")
}

func TestNote(t *testing.T) {
	e := setup(t)
	e.ok("add", "Rule", "--sphere", "perso")
	e.fails(2, "note", "PG-0001", "text")
	got := filepath.Join(e.dir, "note.json")
	script := filepath.Join(e.dir, "note.sh")
	os.WriteFile(script, []byte("#!/bin/sh\ncat > "+got+"\necho kept\n"), 0o755)
	e.appendConfig("notes:\n  run: [\"" + script + "\"]\n")
	r := e.ok("note", "PG-0001", "Not signed.")
	if r["output"] != "kept" {
		t.Fatalf("%v", r)
	}
	var in map[string]any
	b, _ := os.ReadFile(got)
	json.Unmarshal(b, &in)
	if in["text"] != "Not signed." || in["sphere"] != "perso" || in["guard"].(map[string]any)["id"] != "PG-0001" {
		t.Fatalf("stdin: %s", b)
	}
}

func TestSchemaAndText(t *testing.T) {
	e := setup(t)
	var out, errw bytes.Buffer
	if code := run([]string{"schema", "guard", "fire", "--config", e.cfg}, strings.NewReader(""), &out, &errw); code != 0 || !strings.Contains(out.String(), "guard fire <key>") {
		t.Fatalf("%d %s %s", code, out.String(), errw.String())
	}
	e.ok("add", "Rule", "--sphere", "perso", "--ref", "case:a", "--when", "when X calls", "--trigger", "with:contact:X")
	out.Reset()
	run([]string{"ls", "--format", "text", "--config", e.cfg}, strings.NewReader(""), &out, &errw)
	if got := strings.TrimSpace(out.String()); got != "PG-0001  Rule  — when X calls (with:contact:X)  [case:a]" {
		t.Fatalf("%q", got)
	}
}
