package ui

import (
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"

	"github.com/siren403/codex-quota/internal/api"
)

func TestRenderWindowRow_StaysWithinViewportWidth(t *testing.T) {
	model := testModelWithWindows([]api.QuotaWindow{
		{
			Label:       "Weekly usage limit",
			WindowSec:   604800,
			LeftPercent: 0.0,
			ResetAt:     time.Now().Add(48 * time.Hour),
		},
	})
	model.Width = 90

	row := model.renderWindowRow(model.Data.Windows[0])
	if got := ansi.StringWidth(row); got > model.Width {
		t.Fatalf("row width = %d, want <= %d\n%s", got, model.Width, ansi.Strip(row))
	}
}

func TestRenderWindowStatusRow_StaysWithinViewportWidth(t *testing.T) {
	model := testModelWithWindows([]api.QuotaWindow{
		{
			Label:     "Weekly usage limit",
			WindowSec: 604800,
		},
	})
	model.Width = 88

	row := model.renderWindowStatusRow(model.Data.Windows[0], "Loading...")
	if got := ansi.StringWidth(row); got > model.Width {
		t.Fatalf("status row width = %d, want <= %d\n%s", got, model.Width, ansi.Strip(row))
	}
}

func TestRenderWindowRow_PrefersKeepingResetTextComplete(t *testing.T) {
	model := testModelWithWindows([]api.QuotaWindow{
		{
			Label:       "Weekly usage limit",
			WindowSec:   604800,
			LeftPercent: 100.0,
			ResetAt:     time.Now().Add(6*24*time.Hour + 23*time.Hour),
		},
	})
	model.Width = 120

	row := ansi.Strip(model.renderWindowRow(model.Data.Windows[0]))
	if !strings.Contains(row, "Resets ") {
		t.Fatalf("expected reset text in row, got: %q", row)
	}
	if strings.HasSuffix(strings.TrimSpace(row), "...") {
		t.Fatalf("expected reset text to stay complete by shrinking bar first, got: %q", row)
	}
}

func TestRenderWindowRow_LeavesRightEdgeSafetyMargin(t *testing.T) {
	model := testModelWithWindows([]api.QuotaWindow{
		{
			Label:       "Weekly usage limit",
			WindowSec:   604800,
			LeftPercent: 100.0,
			ResetAt:     time.Now().Add(6*24*time.Hour + 23*time.Hour),
		},
	})

	for width := 88; width <= 180; width++ {
		model.Width = width
		row := model.renderWindowRow(model.Data.Windows[0])
		if got := ansi.StringWidth(row); got >= model.Width {
			t.Fatalf("width=%d row width=%d, want < %d\nafter strip: %q", width, got, model.Width, ansi.Strip(row))
		}
	}
}

func TestRenderWindowRow_NarrowWidthKeepsResetSuffixByShrinkingLeftFirst(t *testing.T) {
	model := testModelWithWindows([]api.QuotaWindow{
		{
			Label:       "Weekly usage limit",
			WindowSec:   604800,
			LeftPercent: 100.0,
			ResetAt:     time.Now().Add(6*24*time.Hour + 23*time.Hour),
		},
	})
	model.Width = 100

	row := ansi.Strip(model.renderWindowRow(model.Data.Windows[0]))
	if !strings.Contains(row, "(6d") {
		t.Fatalf("expected reset suffix to remain visible by shrinking left segment first, got: %q", row)
	}
}

func TestRenderWindowRow_VeryNarrowKeepsResetTextAndSuffixVisible(t *testing.T) {
	model := testModelWithWindows([]api.QuotaWindow{
		{
			Label:       "Weekly usage limit",
			WindowSec:   604800,
			LeftPercent: 100.0,
			ResetAt:     time.Now().Add(6*24*time.Hour + 23*time.Hour),
		},
	})
	model.Width = 60

	row := ansi.Strip(model.renderWindowRow(model.Data.Windows[0]))
	if !strings.Contains(row, "…") {
		t.Fatalf("expected truncated reset text on very narrow width, got: %q", row)
	}
	if !strings.Contains(row, "6d") {
		t.Fatalf("expected remaining duration to stay visible, got: %q", row)
	}
}

func TestRenderWindowRow_NarrowWidthTruncatesResetFromLeftNotWindowLabel(t *testing.T) {
	model := testModelWithWindows([]api.QuotaWindow{
		{
			Label:       "Weekly usage limit",
			WindowSec:   604800,
			LeftPercent: 100.0,
			ResetAt:     time.Now().Add(6*24*time.Hour + 23*time.Hour),
		},
	})
	model.Width = 84

	row := ansi.Strip(model.renderWindowRow(model.Data.Windows[0]))
	if !strings.Contains(row, "Weekly") || !strings.Contains(row, "…") {
		t.Fatalf("expected right-side truncation for window label, got: %q", row)
	}
	if strings.Contains(row, "…y usage limit") {
		t.Fatalf("did not expect left-side truncation for window label, got: %q", row)
	}
	if !strings.Contains(row, "6d") {
		t.Fatalf("expected reset suffix to remain visible, got: %q", row)
	}
}
