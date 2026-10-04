package mention

import (
	_ "embed"
	"fmt"
	"strconv"
	"strings"
	"text/template"
	"time"
	"unicode/utf8"

	"github.com/m-mizutani/goerr/v2"

	"github.com/m-mizutani/robin/pkg/domain/model"
)

//go:embed prompt/system.md
var systemPromptTemplate string

// renderSystemPrompt fills in values decided by the server configuration
// only, so the prompt is the same for every user and run.
func renderSystemPrompt(services []string, settingsURL string) (string, error) {
	tmpl, err := template.New("system").Parse(systemPromptTemplate)
	if err != nil {
		return "", goerr.Wrap(err, "failed to parse agent system prompt")
	}
	var b strings.Builder
	if err := tmpl.Execute(&b, struct {
		Services    string
		SettingsURL string
	}{Services: joinNames(services), SettingsURL: settingsURL}); err != nil {
		return "", goerr.Wrap(err, "failed to render agent system prompt")
	}
	return b.String(), nil
}

// joinNames writes "A", "A and B", or "A, B and C".
func joinNames(names []string) string {
	switch len(names) {
	case 0:
		return ""
	case 1:
		return names[0]
	default:
		return strings.Join(names[:len(names)-1], ", ") + " and " + names[len(names)-1]
	}
}

// slackTSTime converts a Slack ts ("1700000000.000100") to a time.
func slackTSTime(ts string) time.Time {
	sec, _, _ := strings.Cut(ts, ".")
	n, err := strconv.ParseInt(sec, 10, 64)
	if err != nil {
		return time.Time{}
	}
	return time.Unix(n, 0).UTC()
}

type connectionLine struct {
	service string
	status  string
}

// fitThread keeps the newest messages that fit in maxChars characters.
func fitThread(lines []string, maxChars int) []string {
	total := 0
	start := len(lines)
	for start > 0 {
		n := utf8.RuneCountInString(lines[start-1]) + 1
		if total+n > maxChars {
			break
		}
		total += n
		start--
	}
	return lines[start:]
}

// renderRequest builds the first user message of a run.
func renderRequest(thread []model.SlackThreadMessage, connections []connectionLine, requester model.SlackUserID,
	mentionTS, text string, threadCharLimit int, now time.Time) string {
	var b strings.Builder

	var lines []string
	for _, m := range thread {
		var who string
		switch {
		case m.FromRobin:
			who = "Robin"
		case m.UserID != "":
			who = "<@" + string(m.UserID) + ">"
		default:
			who = "bot " + m.BotID
		}
		lines = append(lines, fmt.Sprintf("[%s] %s: %s", slackTSTime(m.TS).Format(time.RFC3339), who, m.Text))
	}
	if lines = fitThread(lines, threadCharLimit); len(lines) > 0 {
		b.WriteString("<thread>\n")
		for _, l := range lines {
			b.WriteString(l + "\n")
		}
		b.WriteString("</thread>\n")
	}

	b.WriteString("<connections>\n")
	for _, c := range connections {
		b.WriteString(c.service + ": " + c.status + "\n")
	}
	b.WriteString("</connections>\n")

	fmt.Fprintf(&b, "<request from=\"<@%s>\" at=\"%s\">\n%s\n</request>\n", requester, slackTSTime(mentionTS).Format(time.RFC3339), text)
	fmt.Fprintf(&b, "Current time: %s", now.UTC().Format(time.RFC3339))
	return b.String()
}
