package model_test

import (
	"strings"
	"testing"
	"time"

	"github.com/m-mizutani/gt"

	"github.com/m-mizutani/robin/pkg/domain/model"
)

func TestNewAgentSessionID(t *testing.T) {
	id, err := model.NewAgentSessionID("T0123", "C0456", "1700000000.000100")
	gt.NoError(t, err)
	gt.Equal(t, id, model.AgentSessionID("T0123-C0456-1700000000000100"))

	_, err = model.NewAgentSessionID("T0123", "C0456", "1700000000")
	gt.Error(t, err)
	_, err = model.NewAgentSessionID("T0123", "X0456", "1700000000.000100")
	gt.Error(t, err)
	_, err = model.NewAgentSessionID("T0123", "C04/56", "1700000000.000100")
	gt.Error(t, err)
}

func validAgentSession() *model.AgentSession {
	now := time.Now()
	return &model.AgentSession{
		TeamID:     "T0123",
		UserID:     "U0123",
		ID:         "T0123-C0456-1700000000000100",
		ChannelID:  "C0456",
		ThreadTS:   "1700000000.000100",
		Generation: "gen",
		CreatedAt:  now,
		UpdatedAt:  now,
		ExpiresAt:  now.Add(time.Hour),
	}
}

func TestAgentSession_Validate(t *testing.T) {
	gt.NoError(t, validAgentSession().Validate())

	cases := map[string]func(s *model.AgentSession){
		"invalid user":            func(s *model.AgentSession) { s.UserID = "x" },
		"invalid ID":              func(s *model.AgentSession) { s.ID = "x" },
		"empty channel":           func(s *model.AgentSession) { s.ChannelID = "" },
		"invalid thread ts":       func(s *model.AgentSession) { s.ThreadTS = "1" },
		"empty generation":        func(s *model.AgentSession) { s.Generation = "" },
		"negative message count":  func(s *model.AgentSession) { s.MessageCount = -1 },
		"lease ID without expiry": func(s *model.AgentSession) { s.LeaseID = "l" },
		"lease expiry without ID": func(s *model.AgentSession) { s.LeaseExpiresAt = time.Now() },
		"empty created_at":        func(s *model.AgentSession) { s.CreatedAt = time.Time{} },
		"empty updated_at":        func(s *model.AgentSession) { s.UpdatedAt = time.Time{} },
		"expires before creation": func(s *model.AgentSession) { s.ExpiresAt = s.CreatedAt },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			s := validAgentSession()
			mutate(s)
			gt.Error(t, s.Validate())
		})
	}
}

func TestAgentSession_ExpiredLeased(t *testing.T) {
	s := validAgentSession()
	gt.False(t, s.Expired(s.CreatedAt))
	gt.True(t, s.Expired(s.ExpiresAt))
	gt.False(t, s.Leased(s.CreatedAt))

	s.LeaseID = "l"
	s.LeaseExpiresAt = s.CreatedAt.Add(time.Minute)
	gt.True(t, s.Leased(s.CreatedAt))
	gt.False(t, s.Leased(s.LeaseExpiresAt))
}

func TestAgentSessionMessage_Validate(t *testing.T) {
	valid := func() *model.AgentSessionMessage {
		return &model.AgentSessionMessage{Generation: "g", Seq: 0, Format: "f", Data: []byte("{}"), ExpiresAt: time.Now()}
	}
	gt.NoError(t, valid().Validate())

	cases := map[string]func(m *model.AgentSessionMessage){
		"empty generation": func(m *model.AgentSessionMessage) { m.Generation = "" },
		"negative seq":     func(m *model.AgentSessionMessage) { m.Seq = -1 },
		"empty format":     func(m *model.AgentSessionMessage) { m.Format = "" },
		"empty data":       func(m *model.AgentSessionMessage) { m.Data = nil },
		"too large data":   func(m *model.AgentSessionMessage) { m.Data = []byte(strings.Repeat("a", 1_000_001)) },
		"empty expires_at": func(m *model.AgentSessionMessage) { m.ExpiresAt = time.Time{} },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			m := valid()
			mutate(m)
			gt.Error(t, m.Validate())
		})
	}
	m := valid()
	m.Data = []byte(strings.Repeat("a", 1_000_000))
	gt.NoError(t, m.Validate())
}
