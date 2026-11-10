package archipelago

import "strconv"

// Discord renders ANSI escape codes inside a code fence tagged "ansi", and
// nowhere else. The palette is fixed: these eight foreground codes are all
// that is available, optionally with 1 for bold.
//
// Gray (30) is deliberately absent. It is close to unreadable on Discord's
// dark theme, which is what most people use.
const (
	ansiReset = "\u001b[0m"
	// ansiBold is color-free emphasis, for headings inside an ansi fence
	// where Discord markdown such as **bold** would show literally.
	ansiBold = "\u001b[1m"
)

// playerPalette is ordered so that adjacent slots look clearly different.
// Combining six hues with normal and bold gives twelve distinguishable
// styles before any repeat.
var playerPalette = []string{
	"\u001b[0;31m", // red
	"\u001b[0;32m", // green
	"\u001b[0;34m", // blue
	"\u001b[0;33m", // yellow
	"\u001b[0;35m", // pink
	"\u001b[0;36m", // cyan
	"\u001b[1;31m", // bold red
	"\u001b[1;32m", // bold green
	"\u001b[1;34m", // bold blue
	"\u001b[1;33m", // bold yellow
	"\u001b[1;35m", // bold pink
	"\u001b[1;36m", // bold cyan
}

// colorForSlot picks a stable color for a slot. Keying on the slot number
// rather than the name means a player who changes their alias keeps their
// color, and everyone sees the same assignment.
func colorForSlot(slot int) string {
	if slot < 0 {
		slot = -slot
	}
	return playerPalette[slot%len(playerPalette)]
}

// colorize wraps text in a slot's color. The reset is mandatory: without it
// the color bleeds into the rest of the line.
func colorize(slot int, text string) string {
	return colorForSlot(slot) + text + ansiReset
}

// colorizeSlotText is colorize for a slot number still held as text, which is
// how message parts carry it.
func colorizeSlotText(raw, text string) string {
	slot, err := strconv.Atoi(raw)
	if err != nil {
		return text
	}
	return colorize(slot, text)
}