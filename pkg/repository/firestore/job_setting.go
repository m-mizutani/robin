package firestore

import (
	"context"
	"time"

	"cloud.google.com/go/firestore"
	"github.com/m-mizutani/goerr/v2"
	"google.golang.org/api/iterator"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/m-mizutani/robin/pkg/domain/interfaces"
	"github.com/m-mizutani/robin/pkg/domain/model"
)

type jobSettingRepository struct {
	client *firestore.Client
}

func (r *jobSettingRepository) doc(key model.UserKey) *firestore.DocumentRef {
	return userDoc(r.client, key).Collection(settingsCollection).Doc(jobSettingDocID)
}

// scheduleDoc lives outside the user's document because the scheduler finds
// due triggers of every user through it.
func (r *jobSettingRepository) scheduleDoc(id model.JobTriggerID) *firestore.DocumentRef {
	return r.client.Collection(schedulesCollection).Doc(string(id))
}

func decodeJobSetting(snap *firestore.DocumentSnapshot, key model.UserKey) (*model.JobSetting, error) {
	var s model.JobSetting
	if err := snap.DataTo(&s); err != nil {
		return nil, goerr.Wrap(err, "failed to decode job setting")
	}
	if s.Key() != key {
		return nil, goerr.Wrap(interfaces.ErrKeyMismatch, "stored job setting belongs to another key")
	}
	return &s, nil
}

func settingVals(key model.UserKey) []goerr.Option {
	return []goerr.Option{goerr.V("team_id", key.TeamID), goerr.V("user_id", key.UserID)}
}

func (r *jobSettingRepository) Get(ctx context.Context, key model.UserKey) (*model.JobSetting, error) {
	if err := key.Validate(); err != nil {
		return nil, goerr.Wrap(err, "invalid user key")
	}
	snap, err := r.doc(key).Get(ctx)
	if err != nil {
		if status.Code(err) == codes.NotFound {
			return nil, goerr.Wrap(interfaces.ErrNotFound, "job setting not found", settingVals(key)...)
		}
		return nil, goerr.Wrap(err, "failed to get job setting", settingVals(key)...)
	}
	return decodeJobSetting(snap, key)
}

func (r *jobSettingRepository) Update(ctx context.Context, key model.UserKey, fn func(current *model.JobSetting) (*model.JobSetting, error)) error {
	if err := key.Validate(); err != nil {
		return goerr.Wrap(err, "invalid user key")
	}
	err := r.client.RunTransaction(ctx, func(ctx context.Context, tx *firestore.Transaction) error {
		var current *model.JobSetting
		snap, err := tx.Get(r.doc(key))
		switch {
		case status.Code(err) == codes.NotFound:
		case err != nil:
			return goerr.Wrap(err, "failed to get job setting")
		default:
			if current, err = decodeJobSetting(snap, key); err != nil {
				return err
			}
		}

		var input *model.JobSetting
		if current != nil {
			input = current.Clone()
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

		if err := tx.Set(r.doc(key), next); err != nil {
			return goerr.Wrap(err, "failed to put job setting")
		}
		for _, w := range scheduleWrites(current, next) {
			if w.entry == nil {
				if err := tx.Delete(r.scheduleDoc(w.id)); err != nil {
					return goerr.Wrap(err, "failed to delete job schedule", goerr.V("trigger_id", w.id))
				}
				continue
			}
			if err := tx.Set(r.scheduleDoc(w.id), w.entry); err != nil {
				return goerr.Wrap(err, "failed to put job schedule", goerr.V("trigger_id", w.id))
			}
		}
		return nil
	})
	if err != nil {
		return goerr.Wrap(err, "failed to update job setting", settingVals(key)...)
	}
	return nil
}

// scheduleWrite sets the entry of a trigger, or deletes it when entry is nil.
type scheduleWrite struct {
	id    model.JobTriggerID
	entry *model.JobScheduleEntry
}

// scheduleWrites lists the schedule entries to write so that they match the
// triggers of next: new or moved triggers are set, removed ones deleted.
func scheduleWrites(current, next *model.JobSetting) []scheduleWrite {
	old := make(map[model.JobTriggerID]time.Time)
	if current != nil {
		for _, t := range current.Triggers {
			old[t.ID] = t.NextRunAt
		}
	}
	var out []scheduleWrite
	for _, e := range next.ScheduleEntries() {
		if at, ok := old[e.TriggerID]; !ok || !at.Equal(e.NextRunAt) {
			out = append(out, scheduleWrite{id: e.TriggerID, entry: e})
		}
		delete(old, e.TriggerID)
	}
	for id := range old {
		out = append(out, scheduleWrite{id: id})
	}
	return out
}

func (r *jobSettingRepository) ListDue(ctx context.Context, now time.Time) ([]*model.JobScheduleEntry, error) {
	// One field in both the filter and the order uses the automatic
	// single-field index.
	iter := r.client.Collection(schedulesCollection).
		Where("NextRunAt", "<=", now).
		OrderBy("NextRunAt", firestore.Asc).
		Documents(ctx)
	defer iter.Stop()
	var out []*model.JobScheduleEntry
	for {
		snap, err := iter.Next()
		if err == iterator.Done {
			break
		}
		if err != nil {
			return nil, goerr.Wrap(err, "failed to list due job schedules", goerr.V("now", now))
		}
		var e model.JobScheduleEntry
		if err := snap.DataTo(&e); err != nil {
			return nil, goerr.Wrap(err, "failed to decode job schedule", goerr.V("doc_id", snap.Ref.ID))
		}
		out = append(out, &e)
	}
	return out, nil
}
