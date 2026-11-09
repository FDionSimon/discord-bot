package archipelago

import (
	"strconv"
	"strings"
)

// Item flag bits, from the protocol's NetworkItem.flags.
const (
	flagProgression = 0b001
	flagUseful      = 0b010
	flagTrap        = 0b100
)

// render turns message parts into readable text, resolving every numeric ID
// against the data package and roster gathered during the handshake.
//
// Unresolvable IDs fall back to their numeric form rather than being dropped:
// a message reading "Unknown item 4208" is still useful, an empty one is not.
func (c *Client) render(parts []JSONMessagePart) string {
	c.mu.RLock()
	defer c.mu.RUnlock()

	var b strings.Builder
	for _, part := range parts {
		switch part.Type {
		case "player_id":
			b.WriteString(c.playerName(part.Text))

		case "item_id":
			b.WriteString(c.lookupName(c.itemNames, part.Player, part.Text, "item"))

		case "location_id":
			b.WriteString(c.lookupName(c.locationNames, part.Player, part.Text, "location"))

		case "player_name", "item_name", "location_name", "entrance_name", "text", "color", "":
			b.WriteString(part.Text)

		default:
			// Unknown part types still carry display text in practice.
			b.WriteString(part.Text)
		}
	}

	return strings.TrimSpace(b.String())
}

// playerName resolves a slot number held as text.
func (c *Client) playerName(raw string) string {
	slot, err := strconv.Atoi(raw)
	if err != nil {
		return raw
	}
	if name, ok := c.slotNames[slot]; ok && name != "" {
		return name
	}
	return "Player " + raw
}

// lookupName resolves an item or location ID. The owning slot decides which
// game's table to search, because IDs are only unique within a game.
func (c *Client) lookupName(tables map[string]map[int64]string, slot int, raw, kind string) string {
	id, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		return raw
	}

	if game, ok := c.slotGames[slot]; ok {
		if table, ok := tables[game]; ok {
			if name, ok := table[id]; ok {
				return name
			}
		}
	}

	// The slot's game is unknown or the ID is missing from its table, which
	// happens with custom apworlds the server did not ship data for. Search
	// every table before giving up.
	for _, table := range tables {
		if name, ok := table[id]; ok {
			return name
		}
	}

	return "unknown " + kind + " " + raw
}

// IsProgression reports whether an item advances the game, which is the usual
// filter for a Discord feed: progression items are the interesting ones.
func IsProgression(item *NetworkItem) bool {
	return item != nil && item.Flags&flagProgression != 0
}

// ItemClass describes an item's flags in a word, for display.
func ItemClass(item *NetworkItem) string {
	if item == nil {
		return ""
	}
	switch {
	case item.Flags&flagTrap != 0:
		return "trap"
	case item.Flags&flagProgression != 0:
		return "progression"
	case item.Flags&flagUseful != 0:
		return "useful"
	default:
		return "filler"
	}
}