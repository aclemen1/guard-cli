package mcpserver

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/aclemen1/guard-cli/internal/config"
	"github.com/aclemen1/guard-cli/internal/store"
)

func TestServeOnlyGivenSpheres(t *testing.T) {
	t.Setenv("GUARD_BY", "agent:office-P-0006")
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.yaml")
	cfg, _ := config.Load(cfgPath)
	for _, s := range []struct{ name, prefix string }{{"perso", "P"}, {"pro", "U"}} {
		root := filepath.Join(dir, s.name)
		if err := store.Init(root, "none"); err != nil {
			t.Fatal(err)
		}
		if err := cfg.AddSphere(s.name, config.Sphere{Root: root, Prefix: s.prefix, VCS: "none"}); err != nil {
			t.Fatal(err)
		}
	}
	ctx := context.Background()
	st, ct := mcp.NewInMemoryTransports()
	if _, err := New(cfgPath, []string{"perso"}).Connect(ctx, st, nil); err != nil {
		t.Fatal(err)
	}
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "test"}, nil).Connect(ctx, ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer cs.Close()

	tools, err := cs.ListTools(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	names := map[string]bool{}
	for _, tl := range tools.Tools {
		names[tl.Name] = true
	}
	for _, want := range []string{"guard_add", "guard_ls", "guard_show", "guard_edit", "guard_lift", "guard_restore", "guard_rm", "guard_fire", "guard_note"} {
		if !names[want] {
			t.Errorf("missing tool %s", want)
		}
	}
	if names["setup_init"] || names["setup_tui"] || names["meta_skill"] {
		t.Error("setup and meta actions must not be served")
	}

	call := func(name string, args map[string]any) (bool, map[string]any) {
		res, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: name, Arguments: args})
		if err != nil {
			t.Fatal(err)
		}
		var env map[string]any
		json.Unmarshal([]byte(res.Content[0].(*mcp.TextContent).Text), &env)
		return !res.IsError, env
	}
	ok, env := call("guard_add", map[string]any{"title": "Never sign", "sphere": "perso", "ref": []string{"case:deposit"}, "trigger": "with:contact:AGENCY"})
	if !ok {
		t.Fatalf("%v", env)
	}
	if by := env["result"].(map[string]any)["by"]; by != "agent:office-P-0006" {
		t.Errorf("by: %v", by)
	}
	if ok, env := call("guard_add", map[string]any{"title": "Elsewhere", "sphere": "pro"}); ok || !strings.Contains(env["error"].(map[string]any)["message"].(string), "not served") {
		t.Errorf("pro is not served: %v", env)
	}
	ok, env = call("guard_ls", map[string]any{"ref": []string{"case:deposit"}})
	if !ok || len(env["result"].([]any)) != 1 {
		t.Errorf("ls: %v", env)
	}
	ok, env = call("guard_fire", map[string]any{"key": "with:contact:AGENCY", "dry-run": true})
	if !ok || len(env["result"].([]any)) != 1 {
		t.Errorf("fire: %v", env)
	}
	if _, err := os.Stat(filepath.Join(dir, "perso", "PG-0001.md")); err != nil {
		t.Fatal(err)
	}
}
