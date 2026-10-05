package memory

import (
	"context"
	"slices"
	"sync"
	"time"

	"github.com/m-mizutani/goerr/v2"

	"github.com/m-mizutani/robin/pkg/domain/interfaces"
	"github.com/m-mizutani/robin/pkg/domain/model"
)

type jobSettingRepository struct {
	mu       sync.Mutex
	settings map[model.UserKey]model.JobSetting
	// schedules is the top-level collection of schedule entries.
	schedules map[model.JobTriggerID]model.JobScheduleEntry
}

func newJobSettingRepository() *jobSettingRepository {
	return &jobSettingRepository{
		settings:  make(map[model.UserKey]model.JobSetting),
		schedules: make(map[model.JobTriggerID]model.JobScheduleEntry),
	}
}

func (r *jobSettingRepository) Get(_ context.Context, key model.UserKey) (*model.JobSetting, error) {
	if err := key.Validate(); err != nil {
		return nil, goerr.Wrap(err, "invalid user key")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	s, ok := r.settings[key]
	if !ok {
		return nil, goerr.Wrap(interfaces.ErrNotFound, "job setting not found",
			goerr.V("team_id", key.TeamID), goerr.V("user_id", key.UserID))
	}
	return s.Clone(), nil
}

func (r *jobSettingRepository) Update(_ context.Context, key model.UserKey, fn func(current *model.JobSetting) (*model.JobSetting, error)) error {
	if err := key.Validate(); err != nil {
		return goerr.Wrap(err, "invalid user key")
	}
	r.mu.Lock()
	defer r.mu.Unlock()

	var current, input *model.JobSetting
	if s, ok := r.settings[key]; ok {
		current = s.Clone()
		input = s.Clone()
	}
	next, err := fn(input)
	if err != nil || next == nil {
		return err
	}
	if next.Key() != key {
		return goerr.Wrap(interfaces.ErrKeyMismatch, "job setting belongs to another key")
	}
	if err := next.Validate(); err != nil {
		return goerr.Wrap(err, "invalid job setting")
	}

	r.settings[key] = *next.Clone()
	if current != nil {
		for _, t := range current.Triggers {
			delete(r.schedules, t.ID)
		}
	}
	for _, e := range next.ScheduleEntries() {
		r.schedules[e.TriggerID] = *e
	}
	return nil
}

func (r *jobSettingRepository) ListDue(_ context.Context, now time.Time) ([]*model.JobScheduleEntry, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []*model.JobScheduleEntry
	for _, e := range r.schedules {
		if !e.NextRunAt.After(now) {
			entry := e
			out = append(out, &entry)
		}
	}
	slices.SortFunc(out, func(a, b *model.JobScheduleEntry) int { return a.NextRunAt.Compare(b.NextRunAt) })
	return out, nil
}
