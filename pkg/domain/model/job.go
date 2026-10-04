package model

import (
	"regexp"
	"slices"
	"time"

	"github.com/m-mizutani/goerr/v2"

	// The runtime image may lack the zoneinfo files, and a job's time zone
	// has to load wherever the scheduler runs.
	_ "time/tzdata"
)

// JobID identifies one job of any user. It is also a Firestore document ID.
type JobID string

var jobIDPattern = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)

func (x JobID) Validate() error {
	if !jobIDPattern.MatchString(string(x)) {
		return goerr.New("invalid job ID", goerr.V("job_id", string(x)))
	}
	return nil
}

// JobKind decides which runner runs a job.
type JobKind string

const (
	// JobKindHello posts a short morning greeting written by the model.
	JobKindHello JobKind = "hello"
)

// JobKinds returns every kind this build defines.
func JobKinds() []JobKind {
	return []JobKind{JobKindHello}
}

var jobKindPattern = regexp.MustCompile(`^[a-z][a-z0-9_]{0,31}$`)

// Validate checks the format only. A stored job may have a kind that a newer
// build defined; the scheduler records such a run as having no runner instead
// of failing to read the job.
func (x JobKind) Validate() error {
	if !jobKindPattern.MatchString(string(x)) {
		return goerr.New("invalid job kind", goerr.V("kind", string(x)))
	}
	return nil
}

// Known reports whether this build defines the kind.
func (x JobKind) Known() bool {
	return slices.Contains(JobKinds(), x)
}

var jobChannelIDPattern = regexp.MustCompile(`^[CG][A-Z0-9]{2,}$`)

// ValidateJobChannelID accepts a public or private channel ID; DMs and group
// DMs are not destinations of a job.
func ValidateJobChannelID(id string) error {
	if !jobChannelIDPattern.MatchString(id) {
		return goerr.New("invalid job channel ID", goerr.V("channel_id", id))
	}
	return nil
}

// DailySchedule runs a job every day at Hour:Minute in TimeZone.
type DailySchedule struct {
	Hour     int
	Minute   int
	TimeZone string // IANA name such as "Asia/Tokyo"
}

func (s DailySchedule) Validate() error {
	if s.Hour < 0 || s.Hour > 23 || s.Minute < 0 || s.Minute > 59 {
		return goerr.New("job time is out of range", goerr.V("hour", s.Hour), goerr.V("minute", s.Minute))
	}
	_, err := s.location()
	return err
}

func (s DailySchedule) location() (*time.Location, error) {
	// LoadLocation takes "" as UTC and "Local" as the zone of the process,
	// which differs between serve and schedule; a schedule names its zone.
	if s.TimeZone == "" || s.TimeZone == "Local" {
		return nil, goerr.New("job time zone is not an IANA name", goerr.V("time_zone", s.TimeZone))
	}
	loc, err := time.LoadLocation(s.TimeZone)
	if err != nil {
		return nil, goerr.Wrap(err, "unknown job time zone", goerr.V("time_zone", s.TimeZone))
	}
	return loc, nil
}

// Next returns the earliest scheduled time strictly after after, in UTC. A
// local time that does not exist on a day (the start of daylight saving time)
// is resolved by time.Date, which applies the offset in effect after the
// change: 02:30 on that day in New York becomes 01:30 EST.
func (s DailySchedule) Next(after time.Time) (time.Time, error) {
	if err := s.Validate(); err != nil {
		return time.Time{}, err
	}
	loc, _ := s.location()
	local := after.In(loc)
	for day := 0; ; day++ {
		t := time.Date(local.Year(), local.Month(), local.Day()+day, s.Hour, s.Minute, 0, 0, loc)
		if t.After(after) {
			return t.UTC(), nil
		}
	}
}

// Job is a task one user scheduled. The scheduler moves NextRunAt and writes
// LastRun; the user creates and deletes the job.
type Job struct {
	TeamID      SlackTeamID
	UserID      SlackUserID
	ID          JobID
	Kind        JobKind
	ChannelID   string
	ChannelName string // as conversations.info returned it when the job was created
	Schedule    DailySchedule
	NextRunAt   time.Time
	LastRun     *JobRunSummary // nil before the first run
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

func (j *Job) Key() UserKey {
	return UserKey{TeamID: j.TeamID, UserID: j.UserID}
}

func (j *Job) Validate() error {
	if err := j.Key().Validate(); err != nil {
		return goerr.Wrap(err, "invalid job key")
	}
	if err := j.ID.Validate(); err != nil {
		return err
	}
	if err := j.Kind.Validate(); err != nil {
		return err
	}
	if err := ValidateJobChannelID(j.ChannelID); err != nil {
		return err
	}
	if j.ChannelName == "" {
		return goerr.New("empty job channel name")
	}
	if err := j.Schedule.Validate(); err != nil {
		return err
	}
	if j.NextRunAt.IsZero() {
		return goerr.New("empty job next_run_at")
	}
	if j.LastRun != nil {
		if err := j.LastRun.validate(); err != nil {
			return err
		}
	}
	if j.CreatedAt.IsZero() || j.UpdatedAt.IsZero() {
		return goerr.New("empty job timestamps")
	}
	return nil
}

// JobRunID identifies one run of a job: its scheduled time in UTC, such as
// "20261005T000000Z". One scheduled time has at most one run.
type JobRunID string

const jobRunIDLayout = "20060102T150405Z"

func NewJobRunID(scheduledAt time.Time) JobRunID {
	return JobRunID(scheduledAt.UTC().Format(jobRunIDLayout))
}

type JobRunStatus string

const (
	JobRunRunning   JobRunStatus = "running"
	JobRunSucceeded JobRunStatus = "succeeded"
	JobRunFailed    JobRunStatus = "failed"
	JobRunSkipped   JobRunStatus = "skipped"
)

// JobRunFailure says why a run failed. It is empty unless the status is
// JobRunFailed.
type JobRunFailure string

const (
	JobRunNoFailure JobRunFailure = ""
	// JobRunNoRunner: this build has no runner for the job's kind.
	JobRunNoRunner JobRunFailure = "no_runner"
	// JobRunRunFailed: the runner returned an error.
	JobRunRunFailed JobRunFailure = "run_failed"
	// JobRunTimedOut: the runner did not finish within the time limit.
	JobRunTimedOut JobRunFailure = "timed_out"
)

func validateJobRunState(status JobRunStatus, failure JobRunFailure, deadline, finishedAt time.Time) error {
	switch status {
	case JobRunRunning:
		if deadline.IsZero() || !finishedAt.IsZero() {
			return goerr.New("running job run needs a deadline and no finished_at")
		}
	case JobRunSucceeded, JobRunFailed, JobRunSkipped:
		if finishedAt.IsZero() {
			return goerr.New("finished job run has no finished_at", goerr.V("status", status))
		}
	default:
		return goerr.New("unknown job run status", goerr.V("status", status))
	}
	switch failure {
	case JobRunNoFailure:
		if status == JobRunFailed {
			return goerr.New("failed job run has no failure")
		}
	case JobRunNoRunner, JobRunRunFailed, JobRunTimedOut:
		if status != JobRunFailed {
			return goerr.New("job run has a failure but did not fail", goerr.V("status", status), goerr.V("failure", failure))
		}
	default:
		return goerr.New("unknown job run failure", goerr.V("failure", failure))
	}
	return nil
}

// JobRun is one run of a job. A run that is still JobRunRunning after its
// Deadline was stopped with its process and is never run again.
type JobRun struct {
	TeamID      SlackTeamID
	UserID      SlackUserID
	JobID       JobID
	ID          JobRunID
	Kind        JobKind
	ScheduledAt time.Time
	StartedAt   time.Time
	Deadline    time.Time // zero unless the run was started
	FinishedAt  time.Time // zero while running
	Status      JobRunStatus
	Failure     JobRunFailure
	MessageTS   string
	Spent       NanoUSD
	ExpiresAt   time.Time
}

func (r *JobRun) Key() UserKey {
	return UserKey{TeamID: r.TeamID, UserID: r.UserID}
}

func (r *JobRun) Validate() error {
	if err := r.Key().Validate(); err != nil {
		return goerr.Wrap(err, "invalid job run key")
	}
	if err := r.JobID.Validate(); err != nil {
		return err
	}
	if err := r.Kind.Validate(); err != nil {
		return err
	}
	if r.ScheduledAt.IsZero() || r.StartedAt.IsZero() {
		return goerr.New("empty job run times")
	}
	if r.ID != NewJobRunID(r.ScheduledAt) {
		return goerr.New("job run ID does not match its scheduled time", goerr.V("run_id", r.ID))
	}
	if err := validateJobRunState(r.Status, r.Failure, r.Deadline, r.FinishedAt); err != nil {
		return err
	}
	if r.Spent < 0 {
		return goerr.New("negative job run spending", goerr.V("spent", r.Spent))
	}
	if !r.ExpiresAt.After(r.StartedAt) {
		return goerr.New("job run expires_at must be after started_at")
	}
	return nil
}

// Summary is the part of the run that the job keeps as its LastRun.
func (r *JobRun) Summary() *JobRunSummary {
	return &JobRunSummary{
		RunID:       r.ID,
		Status:      r.Status,
		Failure:     r.Failure,
		ScheduledAt: r.ScheduledAt,
		Deadline:    r.Deadline,
		FinishedAt:  r.FinishedAt,
	}
}

// JobRunSummary is the last run of a job as the settings page shows it.
type JobRunSummary struct {
	RunID       JobRunID
	Status      JobRunStatus
	Failure     JobRunFailure
	ScheduledAt time.Time
	Deadline    time.Time
	FinishedAt  time.Time
}

func (s *JobRunSummary) validate() error {
	if s.RunID == "" || s.ScheduledAt.IsZero() {
		return goerr.New("job last run has no run ID or scheduled time")
	}
	return validateJobRunState(s.Status, s.Failure, s.Deadline, s.FinishedAt)
}

// JobScheduleEntry lets the scheduler find due jobs of every user. It holds
// only the owner, the job ID and the next run time, and it is written in the
// same transaction as its job.
type JobScheduleEntry struct {
	TeamID    SlackTeamID
	UserID    SlackUserID
	JobID     JobID
	NextRunAt time.Time
}

func (e *JobScheduleEntry) Key() UserKey {
	return UserKey{TeamID: e.TeamID, UserID: e.UserID}
}

func (e *JobScheduleEntry) Validate() error {
	if err := e.Key().Validate(); err != nil {
		return goerr.Wrap(err, "invalid job schedule key")
	}
	if err := e.JobID.Validate(); err != nil {
		return err
	}
	if e.NextRunAt.IsZero() {
		return goerr.New("empty job schedule next_run_at")
	}
	return nil
}

// ScheduleEntry is the job's entry for the scheduler.
func (j *Job) ScheduleEntry() *JobScheduleEntry {
	return &JobScheduleEntry{TeamID: j.TeamID, UserID: j.UserID, JobID: j.ID, NextRunAt: j.NextRunAt}
}

// JobClaimRequest claims the run of a job scheduled at ScheduledAt. The claim
// writes Run and moves the job to NextRunAt.
type JobClaimRequest struct {
	JobID       JobID
	ScheduledAt time.Time
	NextRunAt   time.Time
	Run         *JobRun
	Now         time.Time
}

func (r *JobClaimRequest) Validate() error {
	if err := r.JobID.Validate(); err != nil {
		return err
	}
	if !r.NextRunAt.After(r.ScheduledAt) {
		return goerr.New("job next run must be after the claimed run",
			goerr.V("scheduled_at", r.ScheduledAt), goerr.V("next_run_at", r.NextRunAt))
	}
	if r.Now.IsZero() {
		return goerr.New("empty job claim time")
	}
	if r.Run == nil {
		return goerr.New("job claim has no run")
	}
	if r.Run.JobID != r.JobID || !r.Run.ScheduledAt.Equal(r.ScheduledAt) {
		return goerr.New("job claim run does not match the claim", goerr.V("run_id", r.Run.ID))
	}
	return r.Run.Validate()
}
