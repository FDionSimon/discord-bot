package archipelago

import (
	"strings"
	"time"
)

// Per-player play time.
//
// Three signals drive each slot's clock, because no single one is reliable:
//
//   - Join carries the connecting client's tags, so a real game client can be
//     told apart from a tracker or a text client. This starts the clock.
//   - Part says a client for that slot went away. It carries no tags of its
//     own, but the server prints them in the message text, which is enough to
//     ignore a tracker disconnecting. This stops the clock.
//   - The client-status key seeds the clock on connect, since a player who was
//     already playing before the bot started sends no Join.
//
// The status key alone would not do. on_client_left only resets a slot to
// CLIENT_UNKNOWN once *every* client for it has disconnected, and this relay
// is itself a client of whichever slot it authenticates as. For that one slot
// the status sticks at CLIENT_PLAYING, so it is excluded from status seeding
// and driven by Join and Part alone.
//
// Time only accrues while the bot is connected and watching. A multiworld
// played while the bot was down is time this cannot know about, so these
// figures are a floor, not an exact total.

// nonGameTags mirrors MultiServer._non_game_messages. A client carrying any of
// them is watching rather than playing, so its connection must not start a
// clock. Game-side tags such as DeathLink are deliberately absent.
var nonGameTags = []string{"TextOnly", "Tracker", "HintGame"}

// SetOwnSlot records which slot this relay authenticates as, whose status
// cannot be trusted as a presence signal.
func (p *Playthrough) SetOwnSlot(slot int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.ownSlot = slot
	p.haveOwnSlot = true
}

// Joined starts a slot's clock when a real game client connects.
func (p *Playthrough) Joined(slot int, tags []string) {
	for _, tag := range tags {
		for _, skip := range nonGameTags {
			if strings.EqualFold(tag, skip) {
				return
			}
		}
	}

	p.mu.Lock()
	p.startClock(slot)
	p.mu.Unlock()
	p.save(false)
}

// Parted stops a slot's clock when a game client disconnects.
//
// The Part packet carries no tags, so the rendered text is inspected instead:
// the server appends the leaving client's tag list to the message, which is
// enough to tell a player quitting from a tracker closing.
func (p *Playthrough) Parted(slot int, text string) {
	for _, tag := range nonGameTags {
		if strings.Contains(text, tag) {
			return
		}
	}

	p.mu.Lock()
	p.stopClock(slot)
	p.mu.Unlock()
	p.save(false)
}

// --- clock mechanics ------------------------------------------------------
//
// All of these run with the lock held. Elapsed time is folded into the total
// whenever it is observed rather than only when the clock stops, so a restart
// loses at most one poll interval instead of a whole session.

// startClock begins timing a slot, unless it is already running or the slot
// has finished.
func (p *Playthrough) startClock(slot int) {
	if containsSlot(p.state.Goaled, slot) {
		return
	}
	if _, running := p.playingSince[slot]; running {
		p.foldClock(slot)
		return
	}
	if p.playingSince == nil {
		p.playingSince = map[int]time.Time{}
	}
	p.playingSince[slot] = time.Now()
}

// foldClock banks the time elapsed so far and keeps the clock running.
func (p *Playthrough) foldClock(slot int) {
	since, running := p.playingSince[slot]
	if !running {
		return
	}

	// Bank only whole seconds and carry the remainder by advancing the start
	// marker exactly that far. Truncating to time.Now() instead would discard
	// a fraction of a second on every fold, which at one fold per poll adds up
	// to minutes lost over a long session.
	whole := time.Since(since).Truncate(time.Second)
	if whole <= 0 {
		return
	}
	if p.state.PlaytimeSeconds == nil {
		p.state.PlaytimeSeconds = map[int]int64{}
	}
	p.state.PlaytimeSeconds[slot] += int64(whole / time.Second)
	p.playingSince[slot] = since.Add(whole)
	p.dirty = true
}

// stopClock banks the elapsed time and stops timing.
func (p *Playthrough) stopClock(slot int) {
	if _, running := p.playingSince[slot]; !running {
		return
	}
	p.foldClock(slot)
	delete(p.playingSince, slot)
}

// playtime returns a slot's total observed play time, including any time on a
// clock that is still running.
func (p *Playthrough) playtime(slot int) time.Duration {
	total := time.Duration(p.state.PlaytimeSeconds[slot]) * time.Second
	if since, running := p.playingSince[slot]; running {
		total += time.Since(since)
	}
	return total
}

// playtimeLabel renders a slot's play time, or "" when none was observed.
func (p *Playthrough) playtimeLabel(slot int) string {
	d := p.playtime(slot)
	if d < time.Minute {
		return ""
	}
	return humanizeDuration(d)
}