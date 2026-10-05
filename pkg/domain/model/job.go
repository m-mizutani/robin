package model

import (
	"regexp"
	"slices"
	"time"

	"github.com/m-mizutani/goerr/v2"

	// The runtime image may lack the zoneinfo files, and a user's time zone
	// has to load wherever the scheduler runs.
	_ "time/tzdata"
)

// JobName names a job, a unit of work the scheduler starts. Jobs are defined
// in code and not stored.
type JobName string

const (
	// JobNameHello posts a short morning greeting written by the model.
	JobNameHello JobName = "hello"
)

// JobNames returns every job this build defines.
func JobNames() []JobName {
	return []JobName{JobNameHello}
}

var jobNamePattern = regexp.MustCompile(`^[a-z][a-z0-9_]{0,31}$`)

// Validate checks the format only, so a trigger of a job that a newer build
// defined can still be read.
func (x JobName) Validate() error {
	if !jobNamePattern.MatchString(string(x)) {
		return goerr.New("invalid job name", goerr.V("job", string(x)))
	}
	return nil
}

// Known reports whether this build defines the job.
func (x JobName) Known() bool {
	return slices.Contains(JobNames(), x)
}

// DailyTime is a time of day.
type DailyTime struct {
	Hour   int
	Minute int
}

func (t DailyTime) Validate() error {
	if t.Hour < 0 || t.Hour > 23 || t.Minute < 0 || t.Minute > 59 {
		return goerr.New("time of day is out of range", goerr.V("hour", t.Hour), goerr.V("minute", t.Minute))
	}
	return nil
}

// Next returns the earliest time of day t in loc strictly after after, in
// UTC. A local time that does not exist on a day (the start of daylight
// saving time) is resolved by time.Date, which applies the offset in effect
// after the change.
func (t DailyTime) Next(after time.Time, loc *time.Location) time.Time {
	local := after.In(loc)
	for day := 0; ; day++ {
		next := time.Date(local.Year(), local.Month(), local.Day()+day, t.Hour, t.Minute, 0, 0, loc)
		if next.After(after) {
			return next.UTC()
		}
	}
}

// LoadTimeZone loads an IANA time zone. It rejects "" and "Local", which
// time.LoadLocation takes as UTC and as the zone of the process.
func LoadTimeZone(name string) (*time.Location, error) {
	if name == "" || name == "Local" {
		return nil, goerr.New("time zone is not an IANA name", goerr.V("time_zone", name))
	}
	loc, err := time.LoadLocation(name)
	if err != nil {
		return nil, goerr.Wrap(err, "unknown time zone", goerr.V("time_zone", name))
	}
	return loc, nil
}

var jobChannelIDPattern = regexp.MustCompile(`^[CG][A-Z0-9]{2,}$`)

// ValidateJobChannelID accepts a public or private channel ID.
func ValidateJobChannelID(id string) error {
	if !jobChannelIDPattern.MatchString(id) {
		return goerr.New("invalid job channel ID", goerr.V("channel_id", id))
	}
	return nil
}

// JobTriggerID identifies one trigger of any user. It is also the document ID
// of the trigger's schedule entry.
type JobTriggerID string

var jobTriggerIDPattern = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)

func (x JobTriggerID) Validate() error {
	if !jobTriggerIDPattern.MatchString(string(x)) {
		return goerr.New("invalid job trigger ID", goerr.V("trigger_id", string(x)))
	}
	return nil
}

// JobTrigger starts a job every day at Time in the user's time zone.
// NextRunAt is in UTC and always a whole minute.
type JobTrigger struct {
	ID        JobTriggerID
	Job       JobName
	Time      DailyTime
	NextRunAt time.Time
}

func (t *JobTrigger) Validate() error {
	if err := t.ID.Validate(); err != nil {
		return err
	}
	if err := t.Job.Validate(); err != nil {
		return err
	}
	if err := t.Time.Validate(); err != nil {
		return err
	}
	if t.NextRunAt.IsZero() {
		return goerr.New("empty job trigger next_run_at", goerr.V("trigger_id", t.ID))
	}
	return nil
}

// JobSetting is the channel, the time zone and the triggers of one user's
// jobs. It is one document under the user.
type JobSetting struct {
	TeamID    SlackTeamID
	UserID    SlackUserID
	ChannelID string
	TimeZone  string
	Triggers  []JobTrigger
	CreatedAt time.Time
	UpdatedAt time.Time
}

func (s *JobSetting) Key() UserKey {
	return UserKey{TeamID: s.TeamID, UserID: s.UserID}
}

func (s *JobSetting) Validate() error {
	if err := s.Key().Validate(); err != nil {
		return goerr.Wrap(err, "invalid job setting key")
	}
	if err := ValidateJobChannelID(s.ChannelID); err != nil {
		return err
	}
	if _, err := LoadTimeZone(s.TimeZone); err != nil {
		return err
	}
	seen := make(map[JobTriggerID]bool, len(s.Triggers))
	for i := range s.Triggers {
		t := &s.Triggers[i]
		if err := t.Validate(); err != nil {
			return err
		}
		if seen[t.ID] {
			return goerr.New("duplicate job trigger ID", goerr.V("trigger_id", t.ID))
		}
		seen[t.ID] = true
	}
	if s.CreatedAt.IsZero() || s.UpdatedAt.IsZero() {
		return goerr.New("empty job setting timestamps")
	}
	return nil
}

// Location is the setting's time zone.
func (s *JobSetting) Location() (*time.Location, error) {
	return LoadTimeZone(s.TimeZone)
}

// Trigger returns the trigger of the ID, or nil.
func (s *JobSetting) Trigger(id JobTriggerID) *JobTrigger {
	for i := range s.Triggers {
		if s.Triggers[i].ID == id {
			return &s.Triggers[i]
		}
	}
	return nil
}

// Clone copies the setting so that changes to the copy's triggers do not
// reach the original.
func (s *JobSetting) Clone() *JobSetting {
	out := *s
	out.Triggers = slices.Clone(s.Triggers)
	return &out
}

// JobScheduleEntry lets the scheduler find due triggers of every user. It
// holds only the owner, the trigger ID and the next run time.
type JobScheduleEntry struct {
	TeamID    SlackTeamID
	UserID    SlackUserID
	TriggerID JobTriggerID
	NextRunAt time.Time
}

func (e *JobScheduleEntry) Key() UserKey {
	return UserKey{TeamID: e.TeamID, UserID: e.UserID}
}

func (e *JobScheduleEntry) Validate() error {
	if err := e.Key().Validate(); err != nil {
		return goerr.Wrap(err, "invalid job schedule key")
	}
	if err := e.TriggerID.Validate(); err != nil {
		return err
	}
	if e.NextRunAt.IsZero() {
		return goerr.New("empty job schedule next_run_at")
	}
	return nil
}

// ScheduleEntries returns the schedule entry of every trigger of the setting.
func (s *JobSetting) ScheduleEntries() []*JobScheduleEntry {
	out := make([]*JobScheduleEntry, 0, len(s.Triggers))
	for _, t := range s.Triggers {
		out = append(out, &JobScheduleEntry{TeamID: s.TeamID, UserID: s.UserID, TriggerID: t.ID, NextRunAt: t.NextRunAt})
	}
	return out
}
