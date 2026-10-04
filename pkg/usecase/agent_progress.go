package usecase

import (
	"fmt"
	"math/rand/v2"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/m-mizutani/robin/pkg/domain/model"
)

// startPhrases are the first text of a progress message, one chosen at random
// per mention.
var startPhrases = []string{
	"Thinking...",
	"Pondering...",
	"Mulling it over...",
	"Looking into it...",
	"Getting my bearings...",
	"Reading the thread...",
	"Working on it...",
	"Gathering context...",
	"Connecting the dots...",
	"On it...",
	"Taking a look...",
	"Sorting things out...",
	"Collecting my thoughts...",
	"Considering the request...",
	"Digging in...",
	"Piecing it together...",
}

func startPhrase() string {
	return startPhrases[rand.IntN(len(startPhrases))]
}

const (
	// progressNoteChars bounds a note of the model shown as progress.
	progressNoteChars = 280
	// progressQueryChars bounds a search term shown as progress.
	progressQueryChars = 60
)

func truncateText(s string, n int) string {
	s = strings.TrimSpace(s)
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	return string([]rune(s)[:n-1]) + "…"
}

// progressNote shows one line of the model's note.
func progressNote(text string) string {
	return ":thought_balloon: " + truncateText(strings.Join(strings.Fields(text), " "), progressNoteChars)
}

func progressStart() string {
	return ":thought_balloon: " + startPhrase()
}

func progressTool(description string) string {
	return ":mag: " + description
}

const (
	progressSignIn       = ":lock: Sign in to Robin to use me"
	progressStartFailed  = ":warning: Couldn't start this request. Please mention me again."
	progressBusy         = ":hourglass_flowing_sand: Still working on your previous request in this thread"
	progressNotOwner     = ":no_entry_sign: Only the person who started this conversation can continue it"
	progressIncompatible = ":no_entry_sign: Can't continue this conversation"
)

func plural(n int, word string) string {
	if n == 1 {
		return fmt.Sprintf("%d %s", n, word)
	}
	return fmt.Sprintf("%d %ss", n, word)
}

// runStats is the summary every final progress line ends with.
func runStats(llmCalls, toolCalls int, spent model.NanoUSD) string {
	return plural(llmCalls, "LLM call") + " · " + plural(toolCalls, "tool call") + " · " + spent.USD()
}

var concludeProgressTexts = map[concludeReason]string{
	concludeCost:    " · wrapped up near the cost limit",
	concludeCalls:   " · wrapped up at the model call limit",
	concludeHistory: " · wrapped up at the size limit of a conversation",
}

func progressDone(stats string, reason concludeReason) string {
	return ":white_check_mark: Done · " + stats + concludeProgressTexts[reason]
}

// progressNotSaved is shown when the answer was posted but the conversation
// could not be stored, so the next mention does not see this exchange.
func progressNotSaved(stats string) string {
	return ":warning: Answered, but this conversation could not be saved · " + stats
}

func progressNoAnswer(stats string) string { return ":warning: No answer · " + stats }

func progressCostLimit(limit model.NanoUSD, stats string) string {
	return fmt.Sprintf(":money_with_wings: Stopped at the cost limit (%s) · %s", limit.USD(), stats)
}

func progressDeclined(stats string) string { return ":no_entry_sign: Declined · " + stats }
func progressFailed(stats string) string   { return ":warning: Failed · " + stats }

func progressTimedOut(timeout time.Duration, stats string) string {
	return fmt.Sprintf(":warning: Timed out after %s · %s", formatDuration(timeout), stats)
}

// formatDuration writes a whole number of minutes as "10m".
func formatDuration(d time.Duration) string {
	if d%time.Minute == 0 {
		return fmt.Sprintf("%dm", int(d/time.Minute))
	}
	return d.String()
}

// Replies posted in the thread or sent as ephemeral messages.
const (
	notOwnerText = "In this thread, I only answer the person who started the conversation with me. " +
		"Mention me in a new thread to start your own."
	declinedText        = "I can't help with this request."
	noAnswerText        = "I couldn't write an answer to this request. Please ask again, or narrow it down."
	failedText          = "I couldn't finish this request because of an internal error."
	incompatibleText    = "This conversation was started with a different model setting and can't be continued. Mention me in a new thread."
	truncatedAnswerNote = "_(The answer was cut off because it was too long.)_"
)

func costLimitText(limit model.NanoUSD) string {
	return fmt.Sprintf("I reached the cost limit for one request (%s) before I could write an answer. Try a narrower request.", limit.USD())
}

func timedOutText(timeout time.Duration) string {
	return fmt.Sprintf("I couldn't finish this request within %s.", humanDuration(timeout))
}

// humanDuration writes a whole number of minutes as "10 minutes".
func humanDuration(d time.Duration) string {
	if d%time.Minute == 0 {
		return plural(int(d/time.Minute), "minute")
	}
	return d.String()
}
