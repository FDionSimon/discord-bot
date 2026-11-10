package archipelago

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// Playthrough marks the start and the end of one multiworld.
//
// Goal completion is read from data storage (_read_client_status_{team}_{slot}),
// which the server persists in its savefile. That makes it reliable: the state
// can be read at any time, so a bot that was offline when someone finished
// still sees it on reconnect.
//
// Releases and collects are not stored anywhere. The server announces them as
// PrintJSON events and keeps no readable record, so they can only be counted
// while this client is connected. The completion banner therefore turns on
// goals alone, and reports release and collect counts as "seen so far".
//
// State is persisted to a small JSON file so that restarting the bot does not
// re-announce a start or a finish that has already been posted.
type Playthrough struct {
	log  *slog.Logger
	emit func(Event)
	path string // state file; empty keeps state in memory only

	mu      sync.Mutex
	roster  []Participant
	state   playthroughState
	dirty   bool
	lastErr string // last save error, logged once rather than every write

	// playingSince holds the start of each slot's running clock. It is not
	// persisted on purpose: after a restart the bot cannot know whether anyone
	// is still playing, and counting its own downtime as play time would be
	// worse than losing the few seconds since the last fold.
	playingSince map[int]time.Time
	// lastSave throttles writes, since folding a clock dirties the state on
	// every poll.
	lastSave time.Time

	// ownSlot is the slot this relay authenticates as. Its client status is
	// not a usable presence signal, because the relay itself keeps the slot
	// occupied, so the server never resets it to CLIENT_UNKNOWN.
	ownSlot     int
	haveOwnSlot bool
}

// Participant is one player slot in the multiworld.
type Participant struct {
	Slot int
	Name string
	Game string
}

// playthroughState is what survives a restart.
type playthroughState struct {
	SeedName  string    `json:"seed_name"`
	FirstSeen time.Time `json:"first_seen"`
	// Goaled, Released and Collected hold slot numbers, sorted.
	Goaled    []int `json:"goaled"`
	Released  []int `json:"released"`
	Collected []int `json:"collected"`
	// PlaytimeSeconds is observed play time per slot. Integer keys marshal as
	// JSON strings, which is fine: nothing else reads this file.
	PlaytimeSeconds map[int]int64 `json:"playtime_seconds"`
	// Announced flags keep each banner to one posting per seed.
	AnnouncedStart    bool `json:"announced_start"`
	AnnouncedFinish   bool `json:"announced_finish"`
	AnnouncedReleased bool `json:"announced_all_released"`
}

// NewPlaythrough builds a tracker and loads any saved state.
//
// path may be empty, in which case nothing is persisted and a restart will
// re-announce the current seed once.
func NewPlaythrough(path string, emit func(Event), log *slog.Logger) *Playthrough {
	p := &Playthrough{log: log, emit: emit, path: path}
	p.load()
	return p
}

// SetRoster records the current player list. Called on every roster update, so
// an alias change is reflected in later messages.
func (p *Playthrough) SetRoster(roster []Participant) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.roster = roster
}

// Begin notes which multiworld is being watched, announcing it once per seed.
//
// A seed name that differs from the saved one means a different multiworld, so
// everything resets. That is also how a host loading a new game is detected.
func (p *Playthrough) Begin(seed string) {
	if seed == "" {
		return
	}

	p.mu.Lock()

	if p.state.SeedName == seed {
		// Same multiworld as the saved state, so nothing to announce. This is
		// the normal path on every reconnect.
		p.mu.Unlock()
		return
	}

	// A first run cannot tell whether the game started just now or days ago,
	// so it only says what it knows. A seed that changed under us genuinely is
	// a new multiworld.
	heading := "\u25b6  NOW TRACKING MULTIWORLD"
	if p.state.SeedName != "" {
		p.log.Info("new multiworld seed", "old", p.state.SeedName, "new", seed)
		heading = "\u25b6  NEW MULTIWORLD STARTED"
	}

	p.state = playthroughState{SeedName: seed, FirstSeen: time.Now()}
	p.playingSince = nil
	p.dirty = true

	banner := p.banner(heading, "")
	p.state.AnnouncedStart = true
	p.mu.Unlock()

	p.post([]string{banner})
	p.save(true)
}

// Status records one slot's client status, driving the clock and announcing
// progress and completion.
//
// prime is true for the first snapshot after connecting: existing goals are
// recorded without a progress line for each, since they already happened, and
// a slot found already playing has its clock started. A completed multiworld
// is still announced on that first snapshot, because the finish is the thing
// worth not missing.
func (p *Playthrough) Status(slot, status int, prime bool) {
	p.mu.Lock()

	switch status {
	case StatusGoal:
		p.stopClock(slot)

	case StatusUnknown:
		// Every client for this slot has disconnected.
		p.stopClock(slot)

	case StatusPlaying:
		// Seeding from status is only safe for slots this relay does not
		// occupy; see playtime.go. Afterwards Join drives the start, and this
		// call just banks the time elapsed since the last observation.
		if prime && (!p.haveOwnSlot || slot != p.ownSlot) {
			p.startClock(slot)
		}
		p.foldClock(slot)
	}
	// CLIENT_CONNECTED and CLIENT_READY leave the clock as it is: a game
	// client that reports nothing further still had its Join counted.

	if status != StatusGoal || containsSlot(p.state.Goaled, slot) {
		p.mu.Unlock()
		p.save(false)
		return
	}

	p.state.Goaled = addSlot(p.state.Goaled, slot)
	p.dirty = true

	players := p.players()
	done := p.countIn(p.state.Goaled)
	name := p.nameOf(slot)
	played := p.playtimeLabel(slot)
	var messages []string

	if !prime && len(players) > 0 {
		messages = append(messages, p.progressLine(done, len(players), name, played))
	}
	if finish := p.finishBanner(); finish != "" {
		messages = append(messages, finish)
		// Releases often land before the last goal, in which case no further
		// release event will arrive to trigger this check on its own.
		if line := p.releasedBanner(); line != "" {
			messages = append(messages, line)
		}
	}
	p.mu.Unlock()

	p.post(messages)
	p.save(true)
}

// Released records that a slot sent out its remaining items.
func (p *Playthrough) Released(slot int) { p.record(true, slot) }

// Collected records that a slot gathered its items from other worlds.
func (p *Playthrough) Collected(slot int) { p.record(false, slot) }

// record adds a slot to one of the live-only sets and, when that completes the
// release sweep, posts the closing line.
//
// The set is selected under the lock rather than passed in by pointer, because
// Begin can replace the whole state value.
func (p *Playthrough) record(release bool, slot int) {
	p.mu.Lock()

	set := &p.state.Collected
	if release {
		set = &p.state.Released
	}
	if containsSlot(*set, slot) {
		p.mu.Unlock()
		return
	}
	*set = addSlot(*set, slot)
	p.dirty = true

	var messages []string
	if line := p.releasedBanner(); line != "" {
		messages = append(messages, line)
	}
	p.mu.Unlock()

	p.post(messages)
	p.save(true)
}

// --- message building -----------------------------------------------------
//
// All of these run with the lock held.

// progressLine reports how many goals are done and who is still playing.
func (p *Playthrough) progressLine(done, total int, who, played string) string {
	// The colored name carries its own reset, so bold has to start after it or
	// it would be cancelled part way through the line.
	var b strings.Builder
	fmt.Fprintf(&b, "%s finished", who)
	if played != "" {
		fmt.Fprintf(&b, " after %s of play", played)
	}
	fmt.Fprintf(&b, " — %s%d of %d goals complete%s", ansiBold, done, total, ansiReset)

	if remaining := p.remaining(); len(remaining) > 0 {
		b.WriteString("\nStill playing: ")
		b.WriteString(strings.Join(remaining, ", "))
	}
	return b.String()
}

// finishBanner returns the completion banner, or "" when the multiworld is not
// finished or the banner has already been posted.
func (p *Playthrough) finishBanner() string {
	players := p.players()
	if len(players) == 0 || p.state.AnnouncedFinish {
		return ""
	}
	for _, player := range players {
		if !containsSlot(p.state.Goaled, player.Slot) {
			return ""
		}
	}

	p.state.AnnouncedFinish = true
	p.dirty = true

	var body strings.Builder
	body.WriteString("Every player has reached their goal.\n\n")
	for _, player := range players {
		fmt.Fprintf(&body, "  %s — %s", colorize(player.Slot, player.Name), player.Game)
		if played := p.playtimeLabel(player.Slot); played != "" {
			fmt.Fprintf(&body, " (%s)", played)
		}
		body.WriteString("\n")
	}

	released := p.countIn(p.state.Released)
	collected := p.countIn(p.state.Collected)
	fmt.Fprintf(&body, "\nReleased: %d of %d · Collected: %d of %d\n",
		released, len(players), collected, len(players))
	// Be explicit about why those two numbers may look low: unlike goals, the
	// server keeps no readable record of them.
	body.WriteString("(play time, releases and collects are only counted while the bot is connected)")

	return p.banner("🏁  MULTIWORLD COMPLETE", body.String())
}

// releasedBanner returns the closing line once every player has released, or
// "" when that has not happened or was already posted.
func (p *Playthrough) releasedBanner() string {
	players := p.players()
	if len(players) == 0 || p.state.AnnouncedReleased || !p.state.AnnouncedFinish {
		return ""
	}
	for _, player := range players {
		if !containsSlot(p.state.Released, player.Slot) {
			return ""
		}
	}

	p.state.AnnouncedReleased = true
	p.dirty = true
	return ansiBold + "📦  All remaining items have been released — nothing left outstanding." + ansiReset
}

// banner wraps a heading, and an optional body, in a rule.
func (p *Playthrough) banner(heading, body string) string {
	const rule = "────────────────────────────────────────"

	var b strings.Builder
	b.WriteString(ansiBold)
	b.WriteString(rule)
	b.WriteString("\n")
	b.WriteString(heading)
	b.WriteString(ansiReset)
	b.WriteString("\n")

	fmt.Fprintf(&b, "Seed: %s", p.state.SeedName)
	if players := p.players(); len(players) > 0 {
		fmt.Fprintf(&b, " · %d players", len(players))
	}
	if age := time.Since(p.state.FirstSeen); p.state.AnnouncedStart && age > time.Minute {
		fmt.Fprintf(&b, " · tracked for %s", humanizeDuration(age))
	}
	b.WriteString("\n")

	if body != "" {
		b.WriteString("\n")
		b.WriteString(body)
		b.WriteString("\n")
	} else if players := p.players(); len(players) > 0 {
		b.WriteString("\n")
		for _, player := range players {
			fmt.Fprintf(&b, "  %s — %s\n", colorize(player.Slot, player.Name), player.Game)
		}
	}

	b.WriteString(ansiBold)
	b.WriteString(rule)
	b.WriteString(ansiReset)
	return b.String()
}

// players returns the roster sorted by slot number.
//
// The roster has already been filtered to real player slots by
// Client.Participants, so item-link groups and spectators are not in it.
func (p *Playthrough) players() []Participant {
	out := append([]Participant(nil), p.roster...)
	sort.Slice(out, func(i, j int) bool { return out[i].Slot < out[j].Slot })
	return out
}

// remaining lists the players who have not goaled yet, colored.
func (p *Playthrough) remaining() []string {
	var out []string
	for _, player := range p.players() {
		if !containsSlot(p.state.Goaled, player.Slot) {
			out = append(out, colorize(player.Slot, player.Name))
		}
	}
	return out
}

// countIn counts how many of the roster's slots appear in set, so a stray slot
// number cannot push a count past the player total.
func (p *Playthrough) countIn(set []int) int {
	n := 0
	for _, player := range p.players() {
		if containsSlot(set, player.Slot) {
			n++
		}
	}
	return n
}

func (p *Playthrough) nameOf(slot int) string {
	for _, player := range p.roster {
		if player.Slot == slot {
			return colorize(slot, player.Name)
		}
	}
	return colorize(slot, fmt.Sprintf("Player %d", slot))
}

// post emits messages outside the lock, since emit can block briefly.
func (p *Playthrough) post(messages []string) {
	for _, text := range messages {
		if text == "" {
			continue
		}
		p.emit(Event{Kind: "Playthrough", Text: text, At: time.Now()})
	}
}

// --- persistence ----------------------------------------------------------

func (p *Playthrough) load() {
	if p.path == "" {
		return
	}

	raw, err := os.ReadFile(p.path)
	if err != nil {
		if !os.IsNotExist(err) {
			p.log.Warn("cannot read playthrough state, starting fresh", "path", p.path, "error", err)
		}
		return
	}

	var state playthroughState
	if err := json.Unmarshal(raw, &state); err != nil {
		p.log.Warn("unreadable playthrough state, starting fresh", "path", p.path, "error", err)
		return
	}

	p.state = state
	p.log.Info("loaded playthrough state",
		"seed", state.SeedName, "goaled", len(state.Goaled), "finished", state.AnnouncedFinish)
}

// save writes the state file, atomically so a crash mid-write cannot leave a
// truncated file that would be discarded on the next start.
//
// urgent is false for routine clock folds, which dirty the state on every poll
// and do not deserve a write each time. Anything announced is urgent, so a
// banner is never posted twice after a restart.
func (p *Playthrough) save(urgent bool) {
	p.mu.Lock()
	const minInterval = 30 * time.Second
	if p.path == "" || !p.dirty || (!urgent && time.Since(p.lastSave) < minInterval) {
		p.mu.Unlock()
		return
	}
	state := p.state
	p.dirty = false
	p.lastSave = time.Now()
	p.mu.Unlock()

	raw, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		p.warnOnce("encode playthrough state", err)
		return
	}

	tmp := p.path + ".tmp"
	if err := os.MkdirAll(filepath.Dir(p.path), 0o755); err != nil {
		p.warnOnce("create state directory", err)
		return
	}
	if err := os.WriteFile(tmp, raw, 0o644); err != nil {
		p.warnOnce("write playthrough state", err)
		return
	}
	if err := os.Rename(tmp, p.path); err != nil {
		p.warnOnce("replace playthrough state", err)
	}
}

// warnOnce logs a repeated save failure only when the message changes, so a
// read-only directory does not fill the log with one line per event.
func (p *Playthrough) warnOnce(what string, err error) {
	msg := what + ": " + err.Error()

	p.mu.Lock()
	repeat := p.lastErr == msg
	p.lastErr = msg
	p.mu.Unlock()

	if !repeat {
		p.log.Warn("playthrough state not saved", "what", what, "path", p.path, "error", err)
	}
}

// --- small helpers --------------------------------------------------------

func containsSlot(set []int, v int) bool {
	for _, x := range set {
		if x == v {
			return true
		}
	}
	return false
}

func addSlot(set []int, v int) []int {
	set = append(set, v)
	sort.Ints(set)
	return set
}

// humanizeDuration renders a span the way a person would say it.
func humanizeDuration(d time.Duration) string {
	switch {
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh %dm", int(d.Hours()), int(d.Minutes())%60)
	default:
		days := int(d.Hours()) / 24
		return fmt.Sprintf("%dd %dh", days, int(d.Hours())%24)
	}
}