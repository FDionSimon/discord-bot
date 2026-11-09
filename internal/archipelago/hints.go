package archipelago

import (
	"encoding/json"
	"fmt"
	"strconv"
	"time"

	"github.com/gorilla/websocket"
)

// Hint watching.
//
// PrintJSON hint messages only reach the slots a hint concerns, so a relay
// connected as one player sees a fraction of what happens. The server also
// mirrors every slot's hints into a read-only data storage key, which any
// client may read. Subscribing to all of them gives the full picture.
//
// The flow is: Get every key once to learn the current state, then SetNotify
// to be told about changes. The initial Get is recorded without emitting
// anything, otherwise connecting mid-game would replay every hint ever made
// into the Discord channel.

// defaultTeam is team 0. Multiworlds with more than one team are rare, and
// slot_info does not report team numbers, so this client assumes one team.
const defaultTeam = 0

// hintID identifies a hint well enough to notice a repeat. The tuple is
// stable: a hint for a given item at a given location between two players is
// the same hint, and only its found status changes.
type hintID struct {
	finding   int
	receiving int
	location  int64
	item      int64
	found     bool
}

func (h Hint) id() hintID {
	return hintID{
		finding:   h.FindingPlayer,
		receiving: h.ReceivingPlayer,
		location:  h.Location,
		item:      h.Item,
		// Including found means a hint that later gets collected is announced
		// a second time, which is usually wanted: "found" is news.
		found: h.Found,
	}
}

// subscribeHints asks for every slot's hints and registers for updates.
// Called once the roster is known, since slot numbers come from slot_info.
func (c *Client) subscribeHints(conn *websocket.Conn) error {
	c.mu.RLock()
	keys := make([]string, 0, len(c.slotGames))
	for slot := range c.slotGames {
		keys = append(keys, HintsKey(defaultTeam, slot))
	}
	c.mu.RUnlock()

	if len(keys) == 0 {
		return nil
	}

	if err := send(conn, Get{Cmd: "Get", Keys: keys}); err != nil {
		return fmt.Errorf("get hints: %w", err)
	}
	if err := send(conn, SetNotify{Cmd: "SetNotify", Keys: keys}); err != nil {
		return fmt.Errorf("subscribe hints: %w", err)
	}

	c.log.Info("watching hints for all slots", "slots", len(keys))
	c.log.Info("subscribing to hints", "keys", keys)
	return nil
}

// ingestHints decodes a hint list and emits the ones not seen before.
//
// When prime is true the hints are recorded silently. That is the initial Get:
// those hints already happened, and announcing them would flood the channel on
// every reconnect.
func (c *Client) ingestHints(raw json.RawMessage, prime bool) {
	if len(raw) == 0 || string(raw) == "null" {
		return
	}

	var hints []Hint
	if err := json.Unmarshal(raw, &hints); err != nil {
		c.log.Warn("unreadable hint list", "error", err)
		return
	}

	for _, h := range hints {
		id := h.id()

		c.mu.Lock()
		seen := c.seenHints[id]
		c.seenHints[id] = true
		c.mu.Unlock()

		if seen || prime {
			continue
		}

		c.emit(Event{
			Kind: "Hint",
			Text: c.renderHint(h),
			At:   time.Now(),
		})
	}
}

// renderHint turns a stored hint into a sentence.
//
// Note which game owns which ID: the item belongs to the receiving player's
// game, the location to the finding player's. Using the wrong one yields a
// plausible but wrong name, which is worse than no name at all.
func (c *Client) renderHint(h Hint) string {
	c.mu.RLock()
	defer c.mu.RUnlock()

	receiver := c.playerName(strconv.Itoa(h.ReceivingPlayer))
	finder := c.playerName(strconv.Itoa(h.FindingPlayer))
	item := c.lookupName(c.itemNames, h.ReceivingPlayer, strconv.FormatInt(h.Item, 10), "item")
	location := c.lookupName(c.locationNames, h.FindingPlayer, strconv.FormatInt(h.Location, 10), "location")

	msg := fmt.Sprintf("%s's **%s** is at %s", receiver, item, location)
	if h.FindingPlayer != h.ReceivingPlayer {
		msg += fmt.Sprintf(" in %s's world", finder)
	}
	if h.Entrance != "" {
		msg += fmt.Sprintf(" (entrance: %s)", h.Entrance)
	}
	if h.Found {
		msg += " — found"
	}

	return msg
}