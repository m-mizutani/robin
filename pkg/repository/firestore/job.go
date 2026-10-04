package firestore

import (
	"context"
	"slices"
	"time"

	"cloud.google.com/go/firestore"
	"github.com/m-mizutani/goerr/v2"
	"google.golang.org/api/iterator"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/m-mizutani/robin/pkg/domain/interfaces"
	"github.com/m-mizutani/robin/pkg/domain/model"
)

type jobRepository struct {
	client *firestore.Client
}

func (r *jobRepository) jobs(key model.UserKey) *firestore.CollectionRef {
	return userDoc(r.client, key).Collection(jobsCollection)
}

func (r *jobRepository) jobDoc(key model.UserKey, id model.JobID) *firestore.DocumentRef {
	return r.jobs(key).Doc(string(id))
}

func (r *jobRepository) runDoc(key model.UserKey, jobID model.JobID, runID model.JobRunID) *firestore.DocumentRef {
	return r.jobDoc(key, jobID).Collection(jobRunsCollection).Doc(string(runID))
}

// scheduleDoc lives outside the user's document because the scheduler finds
// due jobs of every user through it.
func (r *jobRepository) scheduleDoc(id model.JobID) *firestore.DocumentRef {
	return r.client.Collection(schedulesCollection).Doc(string(id))
}

func decodeJob(snap *firestore.DocumentSnapshot, key model.UserKey) (*model.Job, error) {
	var job model.Job
	if err := snap.DataTo(&job); err != nil {
		return nil, goerr.Wrap(err, "failed to decode job", goerr.V("doc_id", snap.Ref.ID))
	}
	if job.Key() != key {
		return nil, goerr.Wrap(interfaces.ErrKeyMismatch, "stored job belongs to another key", goerr.V("doc_id", snap.Ref.ID))
	}
	return &job, nil
}

// getJob reads the job in tx. A missing job is nil without an error.
func (r *jobRepository) getJob(tx *firestore.Transaction, key model.UserKey, id model.JobID) (*model.Job, error) {
	snap, err := tx.Get(r.jobDoc(key, id))
	if err != nil {
		if status.Code(err) == codes.NotFound {
			return nil, nil
		}
		return nil, goerr.Wrap(err, "failed to get job")
	}
	return decodeJob(snap, key)
}

func exists(tx *firestore.Transaction, ref *firestore.DocumentRef) (bool, error) {
	_, err := tx.Get(ref)
	if err == nil {
		return true, nil
	}
	if status.Code(err) == codes.NotFound {
		return false, nil
	}
	return false, goerr.Wrap(err, "failed to get document", goerr.V("path", ref.Path))
}

func jobVals(key model.UserKey, id model.JobID) []goerr.Option {
	return []goerr.Option{goerr.V("team_id", key.TeamID), goerr.V("user_id", key.UserID), goerr.V("job_id", id)}
}

func (r *jobRepository) Create(ctx context.Context, key model.UserKey, job *model.Job, maxJobs int) error {
	if err := key.Validate(); err != nil {
		return goerr.Wrap(err, "invalid user key")
	}
	if job.Key() != key {
		return goerr.Wrap(interfaces.ErrKeyMismatch, "job belongs to another key")
	}
	if err := job.Validate(); err != nil {
		return goerr.Wrap(err, "invalid job")
	}

	err := r.client.RunTransaction(ctx, func(ctx context.Context, tx *firestore.Transaction) error {
		taken, err := exists(tx, r.scheduleDoc(job.ID))
		if err != nil {
			return err
		}
		if !taken {
			taken, err = exists(tx, r.jobDoc(key, job.ID))
			if err != nil {
				return err
			}
		}
		if taken {
			return goerr.Wrap(interfaces.ErrAlreadyExists, "job ID is taken")
		}

		snaps, err := tx.Documents(r.jobs(key)).GetAll()
		if err != nil {
			return goerr.Wrap(err, "failed to count jobs")
		}
		if len(snaps) >= maxJobs {
			return goerr.Wrap(interfaces.ErrJobLimitReached, "user has the most jobs allowed",
				goerr.V("jobs", len(snaps)), goerr.V("max_jobs", maxJobs))
		}

		if err := tx.Create(r.jobDoc(key, job.ID), job); err != nil {
			return goerr.Wrap(err, "failed to create job")
		}
		if err := tx.Create(r.scheduleDoc(job.ID), job.ScheduleEntry()); err != nil {
			return goerr.Wrap(err, "failed to create job schedule")
		}
		return nil
	})
	if err != nil {
		return goerr.Wrap(err, "failed to create job", jobVals(key, job.ID)...)
	}
	return nil
}

func (r *jobRepository) Get(ctx context.Context, key model.UserKey, id model.JobID) (*model.Job, error) {
	if err := key.Validate(); err != nil {
		return nil, goerr.Wrap(err, "invalid user key")
	}
	if err := id.Validate(); err != nil {
		return nil, goerr.Wrap(interfaces.ErrNotFound, "job not found", goerr.V("job_id", id))
	}
	snap, err := r.jobDoc(key, id).Get(ctx)
	if err != nil {
		if status.Code(err) == codes.NotFound {
			return nil, goerr.Wrap(interfaces.ErrNotFound, "job not found", jobVals(key, id)...)
		}
		return nil, goerr.Wrap(err, "failed to get job", jobVals(key, id)...)
	}
	return decodeJob(snap, key)
}

func (r *jobRepository) List(ctx context.Context, key model.UserKey) ([]*model.Job, error) {
	if err := key.Validate(); err != nil {
		return nil, goerr.Wrap(err, "invalid user key")
	}
	iter := r.jobs(key).Documents(ctx)
	defer iter.Stop()
	var out []*model.Job
	for {
		snap, err := iter.Next()
		if err == iterator.Done {
			break
		}
		if err != nil {
			return nil, goerr.Wrap(err, "failed to list jobs", goerr.V("team_id", key.TeamID), goerr.V("user_id", key.UserID))
		}
		job, err := decodeJob(snap, key)
		if err != nil {
			return nil, err
		}
		out = append(out, job)
	}
	slices.SortFunc(out, func(a, b *model.Job) int { return a.CreatedAt.Compare(b.CreatedAt) })
	return out, nil
}

func (r *jobRepository) Delete(ctx context.Context, key model.UserKey, id model.JobID) error {
	if err := key.Validate(); err != nil {
		return goerr.Wrap(err, "invalid user key")
	}
	if err := id.Validate(); err != nil {
		return nil
	}
	err := r.client.RunTransaction(ctx, func(ctx context.Context, tx *firestore.Transaction) error {
		job, err := r.getJob(tx, key, id)
		if err != nil || job == nil {
			return err
		}
		if err := tx.Delete(r.jobDoc(key, id)); err != nil {
			return goerr.Wrap(err, "failed to delete job")
		}
		if err := tx.Delete(r.scheduleDoc(id)); err != nil {
			return goerr.Wrap(err, "failed to delete job schedule")
		}
		return nil
	})
	if err != nil {
		return goerr.Wrap(err, "failed to delete job", jobVals(key, id)...)
	}
	return nil
}

func (r *jobRepository) ListDue(ctx context.Context, now time.Time, limit int) ([]*model.JobScheduleEntry, error) {
	if limit <= 0 {
		return nil, goerr.New("job schedule limit must be positive", goerr.V("limit", limit))
	}
	// One field in both the filter and the order uses the automatic
	// single-field index.
	iter := r.client.Collection(schedulesCollection).
		Where("NextRunAt", "<=", now).
		OrderBy("NextRunAt", firestore.Asc).
		Limit(limit).
		Documents(ctx)
	defer iter.Stop()
	var out []*model.JobScheduleEntry
	for {
		snap, err := iter.Next()
		if err == iterator.Done {
			break
		}
		if err != nil {
			return nil, goerr.Wrap(err, "failed to list due jobs", goerr.V("now", now))
		}
		var e model.JobScheduleEntry
		if err := snap.DataTo(&e); err != nil {
			return nil, goerr.Wrap(err, "failed to decode job schedule", goerr.V("doc_id", snap.Ref.ID))
		}
		out = append(out, &e)
	}
	return out, nil
}

func (r *jobRepository) Claim(ctx context.Context, key model.UserKey, req model.JobClaimRequest) (bool, error) {
	if err := key.Validate(); err != nil {
		return false, goerr.Wrap(err, "invalid user key")
	}
	if err := req.Validate(); err != nil {
		return false, goerr.Wrap(err, "invalid job claim")
	}
	if req.Run.Key() != key {
		return false, goerr.Wrap(interfaces.ErrKeyMismatch, "job run belongs to another key")
	}

	var claimed bool
	err := r.client.RunTransaction(ctx, func(ctx context.Context, tx *firestore.Transaction) error {
		claimed = false
		job, err := r.getJob(tx, key, req.JobID)
		if err != nil {
			return err
		}
		// NextRunAt is always a whole minute, so the microsecond precision
		// of Firestore does not change it.
		if job == nil || !job.NextRunAt.Equal(req.ScheduledAt) {
			return nil
		}
		ran, err := exists(tx, r.runDoc(key, req.JobID, req.Run.ID))
		if err != nil || ran {
			return err
		}

		job.NextRunAt = req.NextRunAt
		job.LastRun = req.Run.Summary()
		job.UpdatedAt = req.Now
		if err := job.Validate(); err != nil {
			return goerr.Wrap(err, "invalid job")
		}
		if err := tx.Create(r.runDoc(key, req.JobID, req.Run.ID), req.Run); err != nil {
			return goerr.Wrap(err, "failed to create job run")
		}
		if err := tx.Set(r.jobDoc(key, req.JobID), job); err != nil {
			return goerr.Wrap(err, "failed to update job")
		}
		if err := tx.Set(r.scheduleDoc(req.JobID), job.ScheduleEntry()); err != nil {
			return goerr.Wrap(err, "failed to update job schedule")
		}
		claimed = true
		return nil
	})
	if err != nil {
		return false, goerr.Wrap(err, "failed to claim job", append(jobVals(key, req.JobID), goerr.V("run_id", req.Run.ID))...)
	}
	return claimed, nil
}

func (r *jobRepository) Finish(ctx context.Context, key model.UserKey, run *model.JobRun) error {
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

	err := r.client.RunTransaction(ctx, func(ctx context.Context, tx *firestore.Transaction) error {
		job, err := r.getJob(tx, key, run.JobID)
		if err != nil {
			return err
		}
		if err := tx.Set(r.runDoc(key, run.JobID, run.ID), run); err != nil {
			return goerr.Wrap(err, "failed to update job run")
		}
		if job == nil || job.LastRun == nil || job.LastRun.RunID != run.ID {
			return nil
		}
		job.LastRun = run.Summary()
		job.UpdatedAt = run.FinishedAt
		if err := job.Validate(); err != nil {
			return goerr.Wrap(err, "invalid job")
		}
		if err := tx.Set(r.jobDoc(key, run.JobID), job); err != nil {
			return goerr.Wrap(err, "failed to update job")
		}
		return nil
	})
	if err != nil {
		return goerr.Wrap(err, "failed to finish job run", append(jobVals(key, run.JobID), goerr.V("run_id", run.ID))...)
	}
	return nil
}
