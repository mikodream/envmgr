package app

import "github.com/egoist/mygo/ui"

// Icons are single-path stroke SVGs, so the text color tints them and one
// file serves light, dark and any accent.

const (
	svgLogo = `<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round"><rect x="3.5" y="3.5" width="17" height="17" rx="5"/><rect x="8.5" y="8.5" width="7" height="7" rx="2"/></svg>`

	svgGauge = `<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round"><rect x="3" y="3" width="7" height="9" rx="2"/><rect x="14" y="3" width="7" height="5" rx="2"/><rect x="14" y="12" width="7" height="9" rx="2"/><rect x="3" y="16" width="7" height="5" rx="2"/></svg>`

	svgFolder = `<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round"><path d="M3 7.5A2.5 2.5 0 0 1 5.5 5h3.2c.6 0 1.2.2 1.7.7l1.1 1.1c.5.4 1 .7 1.7.7h3.3A2.5 2.5 0 0 1 19 10v6.5A2.5 2.5 0 0 1 16.5 19h-11A2.5 2.5 0 0 1 3 16.5z"/></svg>`

	svgLayers = `<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round"><path d="M12 3l8.5 4.5L12 12 3.5 7.5 12 3z"/><path d="M3.5 12.5L12 17l8.5-4.5"/><path d="M3.5 17L12 21.5l8.5-4.5"/></svg>`

	svgDownload = `<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round"><path d="M12 3.5v10"/><path d="M8 10l4 4 4-4"/><path d="M4 17.5v1A2.5 2.5 0 0 0 6.5 21h11a2.5 2.5 0 0 0 2.5-2.5v-1"/></svg>`

	svgSettings = `<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round"><path d="M4 7h9"/><path d="M17 7h3"/><circle cx="15" cy="7" r="2.2"/><path d="M4 17h3"/><path d="M11 17h9"/><circle cx="9" cy="17" r="2.2"/></svg>`

	svgRefresh = `<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round"><path d="M20 11.5a8 8 0 1 0-2.4 5.1"/><path d="M20 4.5v7h-7"/></svg>`

	svgPlus = `<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.9" stroke-linecap="round" stroke-linejoin="round"><path d="M12 5v14"/><path d="M5 12h14"/></svg>`

	svgTerminal = `<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round"><rect x="3" y="4.5" width="18" height="15" rx="3"/><path d="M7.5 10l2.5 2-2.5 2"/><path d="M13 14h4"/></svg>`

	svgTrash = `<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round"><path d="M4 7h16"/><path d="M9.5 7V5.2c0-.7.5-1.2 1.2-1.2h2.6c.7 0 1.2.5 1.2 1.2V7"/><path d="M6.5 7l.8 12.1c0 .8.7 1.4 1.5 1.4h6.4c.8 0 1.5-.6 1.5-1.4L17.5 7"/></svg>`

	svgExternal = `<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round"><path d="M14 4h6v6"/><path d="M20 4l-8.5 8.5"/><path d="M18.5 14.5V18a2 2 0 0 1-2 2h-10a2 2 0 0 1-2-2v-10a2 2 0 0 1 2-2H10"/></svg>`

	svgCheck = `<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M4.5 12.5l4.5 4.5L19.5 6.5"/></svg>`

	svgAlert = `<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round"><path d="M12 4.5l8 14.5H4l8-14.5z"/><path d="M12 10v4"/><path d="M12 16.6v.2"/></svg>`

	svgSearch = `<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round"><circle cx="10.5" cy="10.5" r="6"/><path d="M15 15l4.5 4.5"/></svg>`

	svgChevronDown = `<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round"><path d="M6 9l6 6 6-6"/></svg>`

	svgChevron = `<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.9" stroke-linecap="round" stroke-linejoin="round"><path d="M9.5 6l6 6-6 6"/></svg>`

	svgSun = `<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round"><circle cx="12" cy="12" r="4"/><path d="M12 2.5v2M12 19.5v2M2.5 12h2M19.5 12h2M5.2 5.2l1.4 1.4M17.4 17.4l1.4 1.4M18.8 5.2l-1.4 1.4M6.6 17.4l-1.4 1.4"/></svg>`

	svgMoon = `<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round"><path d="M20 14.5A8.5 8.5 0 0 1 9.5 4a8.5 8.5 0 1 0 10.5 10.5z"/></svg>`

	svgBox = `<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round"><path d="M12 3.2l8 4.3v9L12 20.8l-8-4.3v-9l8-4.3z"/><path d="M4 7.5l8 4.3 8-4.3"/><path d="M12 11.8V20.8"/></svg>`

	svgPlug = `<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round"><path d="M9 3.5v5"/><path d="M15 3.5v5"/><path d="M6.5 8.5h11v3a5.5 5.5 0 0 1-11 0z"/><path d="M12 17v3.5"/></svg>`

	svgInfo = `<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round"><circle cx="12" cy="12" r="8.5"/><path d="M12 11v5.5"/><path d="M12 7.8v.2"/></svg>`

	svgCopy = `<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round"><rect x="9" y="9" width="11" height="11" rx="2.5"/><path d="M15 6.5V6a2 2 0 0 0-2-2H6a2 2 0 0 0-2 2v7a2 2 0 0 0 2 2h.5"/></svg>`

	// A commit graph: one node on the trunk, one on a branch merging back in.
	svgBranch = `<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round"><circle cx="7" cy="5.5" r="2.2"/><circle cx="7" cy="18.5" r="2.2"/><circle cx="17" cy="9.5" r="2.2"/><path d="M7 7.7v8.6"/><path d="M17 11.7c0 3-2.4 4.4-5 4.7-1.6.2-3 .6-3 2.1"/></svg>`

	svgEdit = `<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round"><path d="M4.5 19.5h4l10-10a2.5 2.5 0 0 0-3.5-3.5l-10 10z"/><path d="M14.5 6.5l3.5 3.5"/></svg>`
)

// Icon handles, parsed once: parsing SVG inside a view would cost every
// frame.
var (
	icLogo        = ui.MustParseSVG([]byte(svgLogo))
	icGauge       = ui.MustParseSVG([]byte(svgGauge))
	icFolder      = ui.MustParseSVG([]byte(svgFolder))
	icLayers      = ui.MustParseSVG([]byte(svgLayers))
	icDownload    = ui.MustParseSVG([]byte(svgDownload))
	icSettings    = ui.MustParseSVG([]byte(svgSettings))
	icRefresh     = ui.MustParseSVG([]byte(svgRefresh))
	icPlus        = ui.MustParseSVG([]byte(svgPlus))
	icTerminal    = ui.MustParseSVG([]byte(svgTerminal))
	icTrash       = ui.MustParseSVG([]byte(svgTrash))
	icExternal    = ui.MustParseSVG([]byte(svgExternal))
	icCheck       = ui.MustParseSVG([]byte(svgCheck))
	icAlert       = ui.MustParseSVG([]byte(svgAlert))
	icSearch      = ui.MustParseSVG([]byte(svgSearch))
	icChevron     = ui.MustParseSVG([]byte(svgChevron))
	icChevronDown = ui.MustParseSVG([]byte(svgChevronDown))
	icSun         = ui.MustParseSVG([]byte(svgSun))
	icMoon        = ui.MustParseSVG([]byte(svgMoon))
	icBox         = ui.MustParseSVG([]byte(svgBox))
	icPlug        = ui.MustParseSVG([]byte(svgPlug))
	icBranch      = ui.MustParseSVG([]byte(svgBranch))
	icEdit        = ui.MustParseSVG([]byte(svgEdit))
	icInfo        = ui.MustParseSVG([]byte(svgInfo))
	icCopy        = ui.MustParseSVG([]byte(svgCopy))
)
