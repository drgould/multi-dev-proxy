package tui

import (
	"image/color"

	"charm.land/lipgloss/v2"
)

// theme holds the semantic color tokens, resolved for a light or dark
// terminal background.
type theme struct {
	accent   color.Color
	ok       color.Color
	warn     color.Color
	err      color.Color
	fg       color.Color
	muted    color.Color
	faint    color.Color
	selBg    color.Color
	hoverBg  color.Color
	chromeBg color.Color
}

func newTheme(isDark bool) theme {
	pick := lipgloss.LightDark(isDark)
	return theme{
		accent:   pick(lipgloss.Color("#6023C0"), lipgloss.Color("#7D56F4")),
		ok:       pick(lipgloss.Color("#1F9D55"), lipgloss.Color("#2ECC71")),
		warn:     pick(lipgloss.Color("#B7791F"), lipgloss.Color("#E5C07B")),
		err:      pick(lipgloss.Color("#C53030"), lipgloss.Color("#E06C75")),
		fg:       pick(lipgloss.Color("#222222"), lipgloss.Color("#DDDDDD")),
		muted:    pick(lipgloss.Color("#767676"), lipgloss.Color("#8A8A8A")),
		faint:    pick(lipgloss.Color("#A8A8A8"), lipgloss.Color("#585858")),
		selBg:    pick(lipgloss.Color("#E9E1FB"), lipgloss.Color("#3A2D63")),
		hoverBg:  pick(lipgloss.Color("#EBEBEB"), lipgloss.Color("#303030")),
		chromeBg: pick(lipgloss.Color("#F2F2F2"), lipgloss.Color("#262626")),
	}
}

// styles are the lipgloss styles derived from the theme; all rendering goes
// through these (no package-level style state).
type styles struct {
	fg       lipgloss.Style
	dim      lipgloss.Style
	faint    lipgloss.Style
	active   lipgloss.Style // ● default/active rows
	selected lipgloss.Style // ▸ cursor marker + name
	section  lipgloss.Style // proxy section headers
	header   lipgloss.Style // column headers
	statusOK lipgloss.Style
	statusWarn lipgloss.Style
	statusErr  lipgloss.Style

	tabActive   lipgloss.Style
	tabInactive lipgloss.Style
	tabHover    lipgloss.Style

	rule lipgloss.Style // separator lines

	chrome      lipgloss.Style // header/footer bar base
	chromeTitle lipgloss.Style
	chromeDim   lipgloss.Style
	chromeOK    lipgloss.Style
	chromeWarn  lipgloss.Style
	chromeErr   lipgloss.Style
}

func newStyles(th theme) styles {
	chrome := lipgloss.NewStyle().Background(th.chromeBg)
	return styles{
		fg:         lipgloss.NewStyle().Foreground(th.fg),
		dim:        lipgloss.NewStyle().Foreground(th.muted),
		faint:      lipgloss.NewStyle().Foreground(th.faint),
		active:     lipgloss.NewStyle().Foreground(th.ok).Bold(true),
		selected:   lipgloss.NewStyle().Foreground(th.accent).Bold(true),
		section:    lipgloss.NewStyle().Bold(true).Foreground(th.fg),
		header:     lipgloss.NewStyle().Foreground(th.muted).Bold(true),
		statusOK:   lipgloss.NewStyle().Foreground(th.ok),
		statusWarn: lipgloss.NewStyle().Foreground(th.warn),
		statusErr:  lipgloss.NewStyle().Foreground(th.err),

		tabActive:   lipgloss.NewStyle().Bold(true).Foreground(th.accent).Underline(true),
		tabInactive: lipgloss.NewStyle().Foreground(th.muted),
		tabHover:    lipgloss.NewStyle().Foreground(th.fg).Underline(true),

		rule: lipgloss.NewStyle().Foreground(th.faint),

		chrome:      chrome,
		chromeTitle: chrome.Bold(true).Foreground(th.accent),
		chromeDim:   chrome.Foreground(th.muted),
		chromeOK:    chrome.Foreground(th.ok),
		chromeWarn:  chrome.Foreground(th.warn),
		chromeErr:   chrome.Foreground(th.err),
	}
}
