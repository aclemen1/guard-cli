package tui

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/aclemen1/tuikit"
	"github.com/charmbracelet/x/ansi"

	"github.com/aclemen1/guard-cli/internal/actions"
	"github.com/aclemen1/guard-cli/internal/store"
)

// sideFrom is the width from which the detail sits at the right of the list.
const sideFrom = 140

func (m *model) View() tea.View {
	v := tea.NewView(m.render())
	v.AltScreen = true
	return v
}

func (m *model) render() string {
	w, h := max(m.w, 40), max(m.h, 10)
	lines := m.header(w)
	bodyH := h - len(lines) - 2
	var body []string
	_, hasSel := m.current()
	switch {
	case m.helpOn:
		body = help()
	case m.err != "":
		body = []string{sErr.Render(m.err)}
	case m.cardOn && hasSel:
		body = m.detail(w, bodyH)
	default:
		body = m.panes(w, bodyH)
	}
	lines = append(lines, body...)
	for len(lines) < h-2 {
		lines = append(lines, "")
	}
	lines = append(lines[:h-2], sMuted.Render(strings.Repeat("─", w)), m.footer(w))
	out := block(lines, w, h)
	if m.modal.Open() {
		return tuikit.Overlay(out, m.modal, w, h)
	}
	return out
}

func (m *model) header(w int) []string {
	active, lifted := 0, 0
	for _, it := range m.all {
		if it.State == store.Active {
			active++
		} else {
			lifted++
		}
	}
	var tags []string
	for _, sp := range m.spheres {
		if m.only == "" || m.only == sp {
			tags = append(tags, sphereTag(sp))
		}
	}
	left := sTitle.Render("guard") + sMuted.Render(" · ") + strings.Join(tags, sMuted.Render(" + ")) +
		sMuted.Render("  ") + sText.Render(plural(active, "consigne active", "consignes actives"))
	if lifted > 0 {
		left += sMuted.Render(fmt.Sprintf(" · %d levée", lifted) + map[bool]string{true: "s", false: ""}[lifted > 1])
	}
	right := sMuted.Render("tri " + sorts[m.sortBy] + map[bool]string{true: " ↓", false: ""}[m.rev] + " · v" + actions.Version)
	gap := w - ansi.StringWidth(left) - ansi.StringWidth(right)
	line1 := left + strings.Repeat(" ", max(1, gap)) + right
	var tabs []string
	for i, v := range views {
		label := fmt.Sprintf("%d %s", i+1, v)
		if i == m.view {
			tabs = append(tabs, sSection.Render(label))
		} else {
			tabs = append(tabs, sMuted.Render(label))
		}
	}
	line2 := strings.Join(tabs, "   ")
	if m.filtering {
		line2 += sMuted.Render("   / ") + m.input.View()
	} else if m.filter != "" {
		line2 += sMuted.Render("   filtre : ") + sText.Render(m.filter)
	}
	return []string{line1, line2, ""}
}

func (m *model) footer(w int) string {
	if m.status != "" {
		if m.statusErr {
			return sErr.Render(m.status)
		}
		return sOK.Render(m.status)
	}
	return helpLine("c", "nouveau", "E", "modifier", "e", "lever", "espace", "lever/rétablir", "N", "note", "o", "ouvrir", "/", "filtrer", "?", "aide", "q", "quitter")
}

// panes lays out the list and the detail: side by side when wide, stacked
// when tall enough, the list alone otherwise or when the detail is off.
func (m *model) panes(w, h int) []string {
	_, hasSel := m.current()
	detail := m.detailOn && hasSel
	switch {
	case detail && w >= sideFrom:
		lw := w * 58 / 100
		dw := w - lw - 3
		left, right := m.list(lw, h), m.detail(dw, h)
		out := make([]string, h)
		bar := sMuted.Render(" │ ")
		for i := range out {
			l, r := "", ""
			if i < len(left) {
				l = left[i]
			}
			if i < len(right) {
				r = right[i]
			}
			out[i] = pad(l, lw) + bar + pad(r, dw)
		}
		return out
	case detail && h >= 18:
		lh := h * 55 / 100
		out := m.list(w, lh)
		for len(out) < lh {
			out = append(out, "")
		}
		out = append(out, sMuted.Render(strings.Repeat("┄", w)))
		return append(out, m.detail(w, h-lh-1)...)
	default:
		return m.list(w, h)
	}
}

func sectionName(it actions.Item, multi bool) string {
	name := "Actives"
	if it.State == store.Lifted {
		name = "Levées"
	}
	if multi {
		name += " · " + it.Sphere
	}
	return name
}

// list draws the rules in sections, keeping the selection in sight.
func (m *model) list(w, h int) []string {
	if len(m.items) == 0 {
		if m.filter != "" {
			return []string{sMuted.Render("aucune consigne ne correspond au filtre (esc pour l'effacer)")}
		}
		return []string{sMuted.Render("aucune consigne dans cette vue (c pour en ajouter une)")}
	}
	idW := 8
	refW := 0
	for _, it := range m.items {
		refW = max(refW, ansi.StringWidth(strings.Join(it.Refs, ", ")))
	}
	refW = min(refW, w/4)
	whenW := 0
	for _, it := range m.items {
		whenW = max(whenW, ansi.StringWidth(actions.Situation(it.Guard)))
	}
	whenW = min(whenW, w/3)
	ruleW := max(10, w-idW-whenW-refW-7)

	type row struct {
		text string
		item int
	}
	var rows []row
	head := " " + sMuted.Render(pad("ID", idW)) + "  " + sMuted.Render(pad("Règle", ruleW))
	if whenW > 0 {
		head += "  " + sMuted.Render(pad("Quand", whenW))
	}
	if refW > 0 {
		head += "  " + sMuted.Render(pad("Réf.", refW))
	}
	prev := ""
	for i, it := range m.items {
		if g := group(it); g != prev {
			if prev != "" {
				rows = append(rows, row{"", -1})
			}
			rows = append(rows, row{" " + sSection.Render(sectionName(it, m.multi())), -1})
			prev = g
		}
		st := sText
		if it.State == store.Lifted {
			st = sMuted
		}
		line := " " + sMuted.Render(pad(it.ID, idW)) + "  " + st.Render(pad(it.Title, ruleW))
		if whenW > 0 {
			line += "  " + sMuted.Render(pad(actions.Situation(it.Guard), whenW))
		}
		if refW > 0 {
			line += "  " + sMuted.Render(pad(strings.Join(it.Refs, ", "), refW))
		}
		rows = append(rows, row{line, i})
	}
	bodyH := max(1, h-1)
	selRow := 0
	for r, x := range rows {
		if x.item == m.sel {
			selRow = r
		}
	}
	if selRow < m.top {
		m.top = max(0, selRow-1)
	}
	if selRow >= m.top+bodyH {
		m.top = selRow - bodyH + 1
	}
	out := []string{head}
	for r := m.top; r < len(rows) && len(out) <= bodyH; r++ {
		if rows[r].item == m.sel {
			out = append(out, selectLine(rows[r].text, w))
		} else {
			out = append(out, rows[r].text)
		}
	}
	return out
}

// detail shows every field of the selected rule, then its context and history.
func (m *model) detail(w, h int) []string {
	it, ok := m.current()
	if !ok {
		return nil
	}
	var out []string
	out = append(out, sTitle.Render(it.ID)+"  "+sMuted.Render(stateLabel(it.State)))
	out = append(out, wrap(sBold.Render(it.Title), w)...)
	out = append(out, "")
	row := func(k, v string) {
		if v == "" {
			return
		}
		first := true
		for _, l := range strings.Split(ansi.Wordwrap(v, max(10, w-12), ""), "\n") {
			label := ""
			if first {
				label = k
			}
			out = append(out, sMuted.Render(pad(label, 11))+" "+sText.Render(l))
			first = false
		}
	}
	row("Quand", it.When)
	row("Déclencheur", it.Trigger)
	row("Réfs", strings.Join(it.Refs, ", "))
	row("Source", it.Source)
	row("Sphère", it.Sphere)
	row("Par", it.By)
	row("Créée", short(it.Created))
	row("Levée", short(it.Lifted))
	if strings.TrimSpace(it.Body) != "" {
		out = append(out, "", sSection.Render("Contexte"))
		out = append(out, wrap(it.Body, w)...)
	}
	if len(it.Log) > 0 {
		out = append(out, "", sSection.Render("Historique"))
		for _, l := range it.Log {
			out = append(out, wrap(sMuted.Render(short(l.At)+" "+l.By+" ")+sText.Render(l.What), w)...)
		}
	}
	if m.scroll > 0 {
		m.scroll = min(m.scroll, max(0, len(out)-1))
		out = out[m.scroll:]
	}
	if len(out) > h {
		out = out[:h]
	}
	return out
}

func stateLabel(s string) string {
	if s == store.Lifted {
		return "levée"
	}
	return "active"
}

func short(ts string) string {
	if len(ts) >= 16 {
		return strings.Replace(ts[:16], "T", " ", 1)
	}
	return ts
}

func wrap(s string, w int) []string {
	var out []string
	for _, para := range strings.Split(s, "\n") {
		out = append(out, strings.Split(ansi.Wordwrap(para, max(10, w), ""), "\n")...)
	}
	return out
}

func sphereTag(sp string) string {
	switch sp {
	case "perso":
		return lipStyle(cPerso).Render(sp)
	case "pro":
		return lipStyle(cPro).Render(sp)
	}
	return lipStyle(cOther).Render(sp)
}

func plural(n int, one, many string) string {
	if n == 1 {
		return "1 " + one
	}
	return fmt.Sprintf("%d %s", n, many)
}

func help() []string {
	return []string{
		sSection.Render("Touches"),
		"",
		helpLine("j k", "descendre, monter", "gg G", "début, fin", "ctrl+d ctrl+u", "page", "[ ]", "section"),
		helpLine("tab", "détail", "entrée l", "fiche", "esc h", "retour", "J K", "défiler le détail"),
		helpLine("1 2 3", "actives, levées, toutes", "s", "sphère", "t T", "trier, inverser", "/", "filtrer", "r", "relire"),
		helpLine("c", "nouvelle consigne", "E", "modifier", "e x", "lever", "espace", "lever ou rétablir"),
		helpLine("N", "ajouter une note", "o", "ouvrir la référence", "#", "supprimer définitivement", "?", "aide", "q", "quitter"),
		"",
		sMuted.Render("Une consigne interdit ou conditionne une action ; elle n'en appelle aucune. Ce qui est à faire est une tâche."),
		sMuted.Render("Lever une consigne la garde dans l'historique ; # ne sert qu'à effacer une erreur de saisie."),
	}
}
