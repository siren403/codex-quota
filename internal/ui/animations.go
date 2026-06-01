package ui

import (
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/siren403/codex-quota/internal/api"
)

type compactBarAnimation struct {
	From      float64
	To        float64
	Current   float64
	StartedAt time.Time
	Duration  time.Duration
}

type tabWindowAnimation struct {
	From      float64
	To        float64
	Current   float64
	StartedAt time.Time
	Duration  time.Duration
}

const (
	animationFrameInterval       = 16 * time.Millisecond
	unifiedAnimationDuration     = 1000 * time.Millisecond
	compactLoadAnimationDuration = unifiedAnimationDuration
	tabLoadAnimationDuration     = unifiedAnimationDuration
	tabSwitchAnimationDuration   = unifiedAnimationDuration
)

func (m *Model) ensureAnimationTickCmd() tea.Cmd {
	if !m.hasActiveAnimations() {
		m.animationTicking = false
		return nil
	}
	if m.animationTicking {
		return nil
	}
	m.animationTicking = true
	return animationTickCmd()
}

func (m *Model) startCompactBarAnimation(accountKey string, prevData api.UsageData, hadPrevData bool, nextData api.UsageData, wasLoading bool) {
	if accountKey == "" {
		return
	}
	target, ok := compactPrimaryRatio(nextData)
	if !ok {
		delete(m.compactBarAnimations, accountKey)
		return
	}

	from := 0.0
	if hadPrevData {
		if prevRatio, hasPrevRatio := compactPrimaryRatio(prevData); hasPrevRatio {
			from = prevRatio
		}
	}
	if !hadPrevData || wasLoading {
		from = 0
	}
	if from == target {
		delete(m.compactBarAnimations, accountKey)
		return
	}

	if m.compactBarAnimations == nil {
		m.compactBarAnimations = make(map[string]compactBarAnimation)
	}
	m.compactBarAnimations[accountKey] = compactBarAnimation{
		From:      from,
		To:        target,
		Current:   from,
		StartedAt: time.Now(),
		Duration:  compactLoadAnimationDuration,
	}
}

func (m *Model) advanceCompactBarAnimations(now time.Time) bool {
	if len(m.compactBarAnimations) == 0 {
		return false
	}
	for key, anim := range m.compactBarAnimations {
		if anim.Duration <= 0 {
			delete(m.compactBarAnimations, key)
			continue
		}
		elapsed := now.Sub(anim.StartedAt)
		if elapsed <= 0 {
			continue
		}
		progress := float64(elapsed) / float64(anim.Duration)
		if progress >= 1 {
			delete(m.compactBarAnimations, key)
			continue
		}
		if progress < 0 {
			progress = 0
		}
		eased := 1 - (1-progress)*(1-progress)
		anim.Current = anim.From + (anim.To-anim.From)*eased
		m.compactBarAnimations[key] = anim
	}
	return len(m.compactBarAnimations) > 0
}

func (m Model) compactBarRatio(accountKey string, fallback float64) float64 {
	anim, ok := m.compactBarAnimations[accountKey]
	if !ok {
		return fallback
	}
	return anim.Current
}

func (m *Model) pruneCompactBarAnimations() {
	if len(m.compactBarAnimations) == 0 {
		return
	}
	valid := make(map[string]struct{}, len(m.Accounts))
	for _, acc := range m.Accounts {
		if acc == nil || acc.Key == "" {
			continue
		}
		valid[acc.Key] = struct{}{}
	}
	for key := range m.compactBarAnimations {
		if _, ok := valid[key]; !ok {
			delete(m.compactBarAnimations, key)
		}
	}
}

func (m *Model) clearCompactBarAnimations() {
	if len(m.compactBarAnimations) == 0 {
		m.animationTicking = false
		return
	}
	for key := range m.compactBarAnimations {
		delete(m.compactBarAnimations, key)
	}
	m.animationTicking = false
}

func (m *Model) clearTabWindowAnimations() {
	if len(m.tabWindowAnimations) == 0 {
		m.animationTicking = false
		return
	}
	for key := range m.tabWindowAnimations {
		delete(m.tabWindowAnimations, key)
	}
	m.animationTicking = false
}

func tabWindowKey(accountKey string, window api.QuotaWindow) string {
	return fmt.Sprintf("%s|%d|%s", accountKey, window.WindowSec, strings.TrimSpace(window.Label))
}

func (m *Model) startTabWindowAnimations(accountKey string, prevData api.UsageData, hadPrevData bool, nextData api.UsageData, wasLoading bool, duration time.Duration) {
	if accountKey == "" {
		return
	}
	m.removeTabWindowAnimationsForAccount(accountKey)

	previousByKey := make(map[string]float64, len(prevData.Windows))
	for _, window := range prevData.Windows {
		previousByKey[tabWindowKey(accountKey, window)] = clampRatio(window.LeftPercent / 100)
	}

	for _, window := range nextData.Windows {
		key := tabWindowKey(accountKey, window)
		target := clampRatio(window.LeftPercent / 100)
		from := 0.0
		if hadPrevData && !wasLoading {
			if prev, ok := previousByKey[key]; ok {
				from = prev
			}
		}
		if from == target {
			delete(m.tabWindowAnimations, key)
			continue
		}
		if m.tabWindowAnimations == nil {
			m.tabWindowAnimations = make(map[string]tabWindowAnimation)
		}
		m.tabWindowAnimations[key] = tabWindowAnimation{
			From:      from,
			To:        target,
			Current:   from,
			StartedAt: time.Now(),
			Duration:  duration,
		}
	}
}

func (m *Model) startTabWindowAnimationsFromZero(accountKey string, nextData api.UsageData, duration time.Duration) {
	if accountKey == "" {
		return
	}
	m.removeTabWindowAnimationsForAccount(accountKey)
	for _, window := range nextData.Windows {
		key := tabWindowKey(accountKey, window)
		target := clampRatio(window.LeftPercent / 100)
		if target == 0 {
			delete(m.tabWindowAnimations, key)
			continue
		}
		if m.tabWindowAnimations == nil {
			m.tabWindowAnimations = make(map[string]tabWindowAnimation)
		}
		m.tabWindowAnimations[key] = tabWindowAnimation{
			From:      0,
			To:        target,
			Current:   0,
			StartedAt: time.Now(),
			Duration:  duration,
		}
	}
}

func (m *Model) removeTabWindowAnimationsForAccount(accountKey string) {
	if accountKey == "" || len(m.tabWindowAnimations) == 0 {
		return
	}
	prefix := accountKey + "|"
	for key := range m.tabWindowAnimations {
		if strings.HasPrefix(key, prefix) {
			delete(m.tabWindowAnimations, key)
		}
	}
}

func (m *Model) advanceTabWindowAnimations(now time.Time) bool {
	if len(m.tabWindowAnimations) == 0 {
		return false
	}
	for key, anim := range m.tabWindowAnimations {
		if anim.Duration <= 0 {
			delete(m.tabWindowAnimations, key)
			continue
		}
		elapsed := now.Sub(anim.StartedAt)
		if elapsed <= 0 {
			continue
		}
		progress := float64(elapsed) / float64(anim.Duration)
		if progress >= 1 {
			delete(m.tabWindowAnimations, key)
			continue
		}
		if progress < 0 {
			progress = 0
		}
		eased := 1 - (1-progress)*(1-progress)
		anim.Current = anim.From + (anim.To-anim.From)*eased
		m.tabWindowAnimations[key] = anim
	}
	return len(m.tabWindowAnimations) > 0
}

func (m *Model) advanceAnimations(now time.Time) bool {
	if m.CompactMode {
		return m.advanceCompactBarAnimations(now)
	}
	return m.advanceTabWindowAnimations(now)
}

func (m *Model) hasActiveAnimations() bool {
	if m.CompactMode {
		return len(m.compactBarAnimations) > 0
	}
	return len(m.tabWindowAnimations) > 0
}

func (m Model) tabWindowRatio(accountKey string, window api.QuotaWindow, fallback float64) float64 {
	if accountKey == "" || len(m.tabWindowAnimations) == 0 {
		return fallback
	}
	anim, ok := m.tabWindowAnimations[tabWindowKey(accountKey, window)]
	if !ok {
		return fallback
	}
	return anim.Current
}
