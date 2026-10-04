package usecase

import (
	"fmt"

	"github.com/m-mizutani/robin/pkg/domain/model"
)

type budgetVerdict int

const (
	budgetContinue  budgetVerdict = iota // call with tools
	budgetConclude                       // call once more without tools
	budgetExhausted                      // do not call again
)

// concludeReason is why a run makes its last call without tools.
type concludeReason int

const (
	concludeNone concludeReason = iota
	concludeCost
	concludeCalls
	concludeHistory
)

// budgetMeter adds up the cost of the model calls of one run. It does not
// stop a call in progress: the run is asked to conclude when the spending
// nears the limit, and no call starts once the limit is reached.
type budgetMeter struct {
	rate        model.Rate
	limit       model.NanoUSD
	noticeRatio float64
	maxCalls    int
	calls       int
	spent       model.NanoUSD
	usage       model.LLMUsage
}

func (m *budgetMeter) add(u model.LLMUsage) {
	m.calls++
	m.spent += m.rate.Cost(u)
	m.usage = m.usage.Add(u)
}

func (m *budgetMeter) verdict() budgetVerdict {
	switch {
	case m.spent >= m.limit:
		return budgetExhausted
	case m.reason() != concludeNone:
		return budgetConclude
	default:
		return budgetContinue
	}
}

// reason tells why the next call has to conclude, or concludeNone.
func (m *budgetMeter) reason() concludeReason {
	switch {
	case float64(m.spent) >= float64(m.limit)*m.noticeRatio:
		return concludeCost
	case m.calls >= m.maxCalls-1:
		return concludeCalls
	default:
		return concludeNone
	}
}

var concludeReasonTexts = map[concludeReason]string{
	concludeCost:    "The budget is nearly used up.",
	concludeCalls:   "This is the last model call allowed for this request.",
	concludeHistory: "This request has gathered as much material as Robin can keep.",
}

const concludeInstruction = "Do not call tools. Write the final answer now from\n" +
	"the information you already have, and say what you could not check."

// budgetNotice is the system message that follows every input. The figures
// are those before the call it is sent with.
func budgetNotice(m *budgetMeter, reason concludeReason) string {
	s := fmt.Sprintf("Budget: %s of %s used, %d of %d model calls.", m.spent.USD(), m.limit.USD(), m.calls, m.maxCalls)
	if reason != concludeNone {
		s += "\n" + concludeReasonTexts[reason] + " " + concludeInstruction
	}
	return s
}
