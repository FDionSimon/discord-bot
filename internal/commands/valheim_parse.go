package commands

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/bwmarrin/discordgo"
)

type Player struct {
	Name        string
	X, Y, Z     float64
	HasPosition bool
}

var playerLine = regexp.MustCompile(
	`^\s*(?:\d+[.)]\s*)?` + // optional leading "1." or "1)" index
		`(.+?)\s+` + // name (non-greedy, so it stops at the ID)
		`(?:\s*[:(]\s*([-\d.,\s]+)\s*\)?)?` + // optional position
		`\s*$`,
)

var skipLine = regexp.MustCompile(`(?i)^\s*(players?|name\s+id|-+|=+|\s*)\s*:?\s*$`)

func ParsePlayers(raw string) []Player {
	var players []Player

	for _, line := range strings.Split(strings.ReplaceAll(raw, "\r\n", "\n"), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || skipLine.MatchString(line) {
			continue
		}

		m := playerLine.FindStringSubmatch(line)
		if m == nil {
			continue
		}

		p := Player{
			Name: strings.TrimSpace(m[1]),
		}
		if m[3] != "" {
			if x, y, z, ok := parsePosition(m[3]); ok {
				p.X, p.Y, p.Z, p.HasPosition = x, y, z, true
			}
		}
		if p.Name != "" {
			players = append(players, p)
		}
	}

	return players
}

func parsePosition(s string) (x, y, z float64, ok bool) {
	fields := strings.FieldsFunc(s, func(r rune) bool {
		return r == ',' || r == ' ' || r == '\t'
	})
	if len(fields) < 3 {
		return 0, 0, 0, false
	}

	vals := make([]float64, 3)
	for i := 0; i < 3; i++ {
		v, err := strconv.ParseFloat(strings.TrimSpace(fields[i]), 64)
		if err != nil {
			return 0, 0, 0, false
		}
		vals[i] = v
	}
	return vals[0], vals[1], vals[2], true
}

func PlayerNames(players []Player) []string {
	names := make([]string, 0, len(players))
	for _, p := range players {
		names = append(names, p.Name)
	}
	return names
}

func renderPlayers(out string) *discordgo.MessageEmbed {
	players := ParsePlayers(out)
	if len(players) == 0 {
		return &discordgo.MessageEmbed{
			Title:       "Online Players",
			Description: "Nobody's online.",
			Color:       ColorInfo,
		}
	}

	lines := make([]string, len(players))
	for idx, p := range players {
		lines[idx] = "• " + p.Name
	}

	return &discordgo.MessageEmbed{
		Title:       fmt.Sprintf("Online players (%d)", len(players)),
		Description: strings.Join(lines, "\n"),
		Color:       ColorSuccess,
	}
}
