package tui

import (
	"context"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/kanggu12/wsl-shrinker/internal/engine/shrink"
	"github.com/kanggu12/wsl-shrinker/internal/front/ui"
)

// Snapshot is one frame of the screen, for the README's pictures and the
// tests: the screen at w by h after loading, with keys pressed. When the
// keys start a run, the run plays until the step named stopAt is running
// (or to its end when stopAt is empty).
func Snapshot(b Backend, w, h int, dark bool, keys []string, stopAt string) string {
	m := newModel(context.Background(), b)
	m.width, m.height, m.th = w, h, ui.NewTheme(dark)
	// Load as Init would, but in this goroutine.
	m.info, m.infoKnown = b.Info(m.ctx), true
	next, _ := m.Update(m.loadDisks()())
	m = next.(model)
	m = m.surveyNow()
	for _, k := range keys {
		next, _ = m.Update(snapshotKey(k))
		m = next.(model)
		m = m.surveyNow()
	}
	// Play a run that the keys started.
	if m.mode == modeRun && m.run != nil {
		for !m.run.done {
			msg := <-m.run.events
			if e, ok := msg.(eventMsg); ok && stopAt != "" && e.Step == shrink.Step(stopAt) && e.Status == shrink.Running {
				next, _ = m.Update(msg)
				m = next.(model)
				// Show the step a few seconds in.
				m.now = m.run.stepStart.Add(7 * time.Second)
				m.run.started = m.now.Add(-19 * time.Second)
				break
			}
			next, _ = m.Update(msg)
			m = next.(model)
		}
	}
	return m.View().Content
}

// surveyNow runs a pending survey of the selected disk synchronously.
func (m model) surveyNow() model {
	d, ok := m.selected()
	if !ok || d.Distro == "" || !d.Running {
		return m
	}
	if s := m.surveys[d.Distro]; s != nil && !s.loading {
		return m
	}
	r, err := m.b.Survey(m.ctx, d.Distro)
	m.surveys[d.Distro] = &surveyState{r: r, err: err, at: time.Now()}
	return m
}

// snapshotKey is the key event for a key's name.
func snapshotKey(k string) tea.KeyPressMsg {
	switch k {
	case "enter":
		return tea.KeyPressMsg{Code: tea.KeyEnter}
	case "esc":
		return tea.KeyPressMsg{Code: tea.KeyEscape}
	case "down":
		return tea.KeyPressMsg{Code: tea.KeyDown}
	case "up":
		return tea.KeyPressMsg{Code: tea.KeyUp}
	case "space":
		return tea.KeyPressMsg{Code: ' ', Text: " "}
	// Named keys that would otherwise be read as their first letter.
	case "pgup":
		return tea.KeyPressMsg{Code: tea.KeyPgUp}
	case "pgdown":
		return tea.KeyPressMsg{Code: tea.KeyPgDown}
	case "home":
		return tea.KeyPressMsg{Code: tea.KeyHome}
	case "end":
		return tea.KeyPressMsg{Code: tea.KeyEnd}
	case "tab":
		return tea.KeyPressMsg{Code: tea.KeyTab}
	case "left":
		return tea.KeyPressMsg{Code: tea.KeyLeft}
	case "right":
		return tea.KeyPressMsg{Code: tea.KeyRight}
	}
	r := []rune(k)[0]
	return tea.KeyPressMsg{Code: r, Text: string(r)}
}

// ParseKeys splits a snapshot's keys ("down space s enter").
func ParseKeys(s string) []string { return strings.Fields(s) }
