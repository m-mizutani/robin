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

type jobKey struct {
	user model.UserKey
	id   model.JobID
}

type jobRunKey struct {
	job jobKey
	id  model.JobRunID
}

type jobRepository struct {
	mu   sync.Mutex
	jobs map[jobKey]model.Job
	runs map[jobRunKey]model.JobRun
	// schedules maps a job to its entry, as the top-level collection does.
	schedules map[model.JobID]model.JobScheduleEntry
}

func newJobRepository() *jobRepository {
	return &jobRepository{
		jobs:      make(map[jobKey]model.Job),
		runs:      make(map[jobRunKey]model.JobRun),
		schedules: make(map[model.JobID]model.JobScheduleEntry),
	}
}

// copyJob keeps callers from changing the stored LastRun through the pointer.
func copyJob(j model.Job) *model.Job {
	if j.LastRun != nil {
		last := *j.LastRun
		j.LastRun = &last
	}
	return &j
}

func (r *jobRepository) Create(_ context.Context, key model.UserKey, job *model.Job, maxJobs int) error {
	if err := key.Validate(); err != nil {
		return goerr.Wrap(err, "invalid user key")
	}
	if job.Key() != key {
		return goerr.Wrap(interfaces.ErrKeyMismatch, "job belongs to another key")
	}
	if err := job.Validate(); err != nil {
		return goerr.Wrap(err, "invalid job")
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.jobs[jobKey{user: key, id: job.ID}]; ok {
		return goerr.Wrap(interfaces.ErrAlreadyExists, "job already exists", goerr.V("job_id", job.ID))
	}
	if _, ok := r.schedules[job.ID]; ok {
		return goerr.Wrap(interfaces.ErrAlreadyExists, "job ID is taken", goerr.V("job_id", job.ID))
	}
	count := 0
	for k := range r.jobs {
		if k.user == key {
			count++
		}
	}
	if count >= maxJobs {
		return goerr.Wrap(interfaces.ErrJobLimitReached, "user has the most jobs allowed",
			goerr.V("jobs", count), goerr.V("max_jobs", maxJobs))
	}
	r.jobs[jobKey{user: key, id: job.ID}] = *copyJob(*job)
	r.schedules[job.ID] = *job.ScheduleEntry()
	return nil
}

func (r *jobRepository) Get(_ context.Context, key model.UserKey, id model.JobID) (*model.Job, error) {
	if err := key.Validate(); err != nil {
		return nil, goerr.Wrap(err, "invalid user key")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	job, ok := r.jobs[jobKey{user: key, id: id}]
	if !ok {
		return nil, goerr.Wrap(interfaces.ErrNotFound, "job not found", goerr.V("job_id", id))
	}
	return copyJob(job), nil
}

func (r *jobRepository) List(_ context.Context, key model.UserKey) ([]*model.Job, error) {
	if err := key.Validate(); err != nil {
		return nil, goerr.Wrap(err, "invalid user key")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []*model.Job
	for k, job := range r.jobs {
		if k.user == key {
			out = append(out, copyJob(job))
		}
	}
	slices.SortFunc(out, func(a, b *model.Job) int { return a.CreatedAt.Compare(b.CreatedAt) })
	return out, nil
}

func (r *jobRepository) Delete(_ context.Context, key model.UserKey, id model.JobID) error {
	if err := key.Validate(); err != nil {
		return goerr.Wrap(err, "invalid user key")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	k := jobKey{user: key, id: id}
	if _, ok := r.jobs[k]; !ok {
		return nil
	}
	delete(r.jobs, k)
	delete(r.schedules, id)
	return nil
}

func (r *jobRepository) ListDue(_ context.Context, now time.Time, limit int) ([]*model.JobScheduleEntry, error) {
	if limit <= 0 {
		return nil, goerr.New("job schedule limit must be positive", goerr.V("limit", limit))
	}
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
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func (r *jobRepository) Claim(_ context.Context, key model.UserKey, req model.JobClaimRequest) (bool, error) {
	if err := key.Validate(); err != nil {
		return false, goerr.Wrap(err, "invalid user key")
	}
	if err := req.Validate(); err != nil {
		return false, goerr.Wrap(err, "invalid job claim")
	}
	if req.Run.Key() != key {
		return false, goerr.Wrap(interfaces.ErrKeyMismatch, "job run belongs to another key")
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	k := jobKey{user: key, id: req.JobID}
	job, ok := r.jobs[k]
	if !ok || !job.NextRunAt.Equal(req.ScheduledAt) {
		return false, nil
	}
	rk := jobRunKey{job: k, id: req.Run.ID}
	if _, ok := r.runs[rk]; ok {
		return false, nil
	}
	job.NextRunAt = req.NextRunAt
	job.LastRun = req.Run.Summary()
	job.UpdatedAt = req.Now
	if err := job.Validate(); err != nil {
		return false, goerr.Wrap(err, "invalid job")
	}
	r.runs[rk] = *req.Run
	r.jobs[k] = job
	r.schedules[job.ID] = *job.ScheduleEntry()
	return true, nil
}

func (r *jobRepository) Finish(_ context.Context, key model.UserKey, run *model.JobRun) error {
	if err := key.Validate(); err != nil {
		return goerr.Wrap(err, "invalid user key")
	}
	if run.Key() != key {
		return goerr.Wrap(interfaces.ErrKeyMismatch, "job run belongs to another key")
	}
	if err := run.Validate(); err != nil {
		return goerr.Wrap(err, "invalid job run")
	}
	if run.Status == model.JobRunRunning {
		return goerr.New("job run is not finished", goerr.V("run_id", run.ID))
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	k := jobKey{user: key, id: run.JobID}
	r.runs[jobRunKey{job: k, id: run.ID}] = *run
	job, ok := r.jobs[k]
	if !ok || job.LastRun == nil || job.LastRun.RunID != run.ID {
		return nil
	}
	job.LastRun = run.Summary()
	job.UpdatedAt = run.FinishedAt
	if err := job.Validate(); err != nil {
		return goerr.Wrap(err, "invalid job")
	}
	r.jobs[k] = job
	return nil
}
