// SPDX-License-Identifier: GPL-3.0-only

package notify

import (
	"fmt"
	"strings"
	"time"
)

const (
	templateSeparator = "━━━━━━━━━━━━━━"
	clockEmoji        = "🕐"
	titleEmoji        = "📌"
	fieldEmoji        = "▫️"
)

// Limits on the structured parts a sender may add.
const (
	maxTitleRunes      = 120
	maxFields          = 8
	maxFieldKeyRunes   = 40
	maxFieldValueRunes = 200
)

// Field is one short fact shown under the body ("Phase: 3 of 5").
type Field struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

// layout is everything the WhatsApp template shows.
type layout struct {
	// LabelKey picks the emoji and level word from the locale file: a level name,
	// or a special marker such as heartbeat_lost.
	LabelKey string
	Source   string
	Project  string
	Title    string
	Text     string
	Fields   []Field
	// Agent is true for a message sent by an agent: the header names the agent and the project.
	Agent bool
}

// formattingNeutralizer swaps the WhatsApp formatting characters for look-alikes so
// text typed by a sender cannot bold, italicize, strike, or code-format the layout.
var formattingNeutralizer = strings.NewReplacer("*", "∗", "_", "＿", "~", "∼", "`", "ˋ", "\r\n", " ", "\n", " ", "\r", " ")

// neutralize makes a single-line, formatting-free copy of text.
func neutralize(text string) string {
	return strings.TrimSpace(formattingNeutralizer.Replace(text))
}

// render builds the WhatsApp text:
//
//	header
//	separator
//	[title, blank line]
//	body
//	[blank line, fields]
//	separator
//	time and date
//
// The body keeps the sender's own formatting; the header, title, and fields are neutralized.
func (s *Service) render(l layout, t time.Time) string {
	emoji := s.locale.GetPrefix(l.LabelKey)
	level := s.locale.GetLevel(l.LabelKey)

	var header string
	if l.Agent {
		header = fmt.Sprintf("%s *%s*", emoji, neutralize(s.agentLabel(l.Source)))
		if project := neutralize(l.Project); project != "" {
			header += " · " + project
		}
		if l.LabelKey == "warning" || l.LabelKey == "critical" || l.LabelKey == "heartbeat_lost" {
			header += " · " + level
		}
	} else {
		header = fmt.Sprintf("%s *%s*", emoji, level)
		if source := neutralize(l.Source); source != "" {
			header += " · " + source
		}
	}

	var b strings.Builder
	b.WriteString(header + "\n" + templateSeparator + "\n")
	if title := neutralize(l.Title); title != "" {
		b.WriteString(fmt.Sprintf("%s *%s*\n\n", titleEmoji, title))
	}
	b.WriteString(strings.TrimSpace(l.Text))
	b.WriteString("\n")
	if len(l.Fields) > 0 {
		b.WriteString("\n")
		for _, f := range l.Fields {
			b.WriteString(fmt.Sprintf("%s *%s:* %s\n", fieldEmoji, neutralize(f.Key), neutralize(f.Value)))
		}
	}
	b.WriteString(templateSeparator + "\n")
	b.WriteString(fmt.Sprintf("%s %s", clockEmoji, t.In(s.loc).Format("15:04 · 02/01")))
	return b.String()
}

// validateParts checks the title and fields a sender gave and returns the cleaned fields.
func validateParts(title string, fields []Field) (string, []Field, error) {
	title = strings.TrimSpace(title)
	if len([]rune(title)) > maxTitleRunes {
		return "", nil, fmt.Errorf("title exceeds %d characters limit", maxTitleRunes)
	}
	if len(fields) > maxFields {
		return "", nil, fmt.Errorf("at most %d fields are allowed", maxFields)
	}
	cleaned := make([]Field, 0, len(fields))
	for _, f := range fields {
		key, value := strings.TrimSpace(f.Key), strings.TrimSpace(f.Value)
		if key == "" || value == "" {
			return "", nil, fmt.Errorf("every field needs a key and a value")
		}
		if len([]rune(key)) > maxFieldKeyRunes {
			return "", nil, fmt.Errorf("a field key exceeds %d characters limit", maxFieldKeyRunes)
		}
		if len([]rune(value)) > maxFieldValueRunes {
			return "", nil, fmt.Errorf("a field value exceeds %d characters limit", maxFieldValueRunes)
		}
		cleaned = append(cleaned, Field{Key: key, Value: value})
	}
	return title, cleaned, nil
}

// speechStripper removes WhatsApp formatting marks from text that is spoken.
var speechStripper = strings.NewReplacer("*", "", "~", "", "`", "")

// defaultSpeech is what is spoken when the sender gave no speech text: the title,
// the body, and then fields as "key: value" while they fit in MaxSpeechRunes.
// Separators, emojis, and field markers are never spoken.
func defaultSpeech(title, text string, fields []Field) string {
	var parts []string
	if title != "" {
		parts = append(parts, strings.TrimRight(title, ".!?؟")+".")
	}
	parts = append(parts, strings.TrimSpace(text))
	speech := speechStripper.Replace(strings.Join(parts, " "))
	speech = strings.Join(strings.Fields(speech), " ")
	for _, f := range fields {
		extra := speechStripper.Replace(fmt.Sprintf("%s: %s.", f.Key, f.Value))
		if len([]rune(speech))+1+len([]rune(extra)) > MaxSpeechRunes {
			break
		}
		speech += " " + extra
	}
	return truncateRunes(speech, MaxSpeechRunes)
}

// splitTemplate returns the header and the middle block (title, body, fields) of a
// stored message. Messages written before the template existed have no separators:
// their first line is the header and their last line the time.
func splitTemplate(text string) (header, middle string) {
	lines := strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n")
	if len(lines) == 0 {
		return "", ""
	}
	header = strings.TrimSpace(lines[0])
	start, end := 1, len(lines)
	if len(lines) > 1 && strings.TrimSpace(lines[1]) == templateSeparator {
		start = 2
		for i := len(lines) - 1; i >= start; i-- {
			if strings.TrimSpace(lines[i]) == templateSeparator {
				end = i
				break
			}
		}
	} else if len(lines) > 2 {
		end = len(lines) - 1
	}
	if start > end {
		start = end
	}
	return header, strings.TrimSpace(strings.Join(lines[start:end], "\n"))
}
