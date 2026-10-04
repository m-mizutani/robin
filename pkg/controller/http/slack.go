package http

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"time"

	"github.com/m-mizutani/goerr/v2"
	"github.com/slack-go/slack"
	"github.com/slack-go/slack/slackevents"

	"github.com/m-mizutani/robin/pkg/domain/model"
	"github.com/m-mizutani/robin/pkg/utils/async"
	"github.com/m-mizutani/robin/pkg/utils/errutil"
)

const (
	slackMaxBodyBytes       = 1 << 20
	slackTimestampTolerance = 5 * time.Minute
)

// verifySlackSignature checks the v0 signature Slack attaches to every
// request and rejects timestamps more than five minutes away from now.
func verifySlackSignature(signingSecret, timestamp, signature string, body []byte, now time.Time) error {
	if timestamp == "" {
		return goerr.New("missing slack request timestamp")
	}
	if signature == "" {
		return goerr.New("missing slack signature")
	}

	ts, err := strconv.ParseInt(timestamp, 10, 64)
	if err != nil {
		return goerr.Wrap(err, "invalid slack request timestamp")
	}
	diff := now.Sub(time.Unix(ts, 0))
	if diff < 0 {
		diff = -diff
	}
	if diff > slackTimestampTolerance {
		return goerr.New("slack request timestamp is out of range", goerr.V("timestamp", timestamp))
	}

	mac := hmac.New(sha256.New, []byte(signingSecret))
	mac.Write([]byte("v0:" + timestamp + ":"))
	mac.Write(body)
	expected := "v0=" + hex.EncodeToString(mac.Sum(nil))
	if !hmac.Equal([]byte(expected), []byte(signature)) {
		return goerr.New("slack signature mismatch")
	}
	return nil
}

func slackSignatureMiddleware(signingSecret string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ctx := r.Context()

			body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, slackMaxBodyBytes))
			if err != nil {
				var tooLarge *http.MaxBytesError
				if errors.As(err, &tooLarge) {
					errutil.Handle(ctx, goerr.Wrap(err, "slack request body is too large"), "slack request rejected")
					w.WriteHeader(http.StatusRequestEntityTooLarge)
					return
				}
				errutil.Handle(ctx, goerr.Wrap(err, "failed to read slack request body"), "slack request rejected")
				w.WriteHeader(http.StatusBadRequest)
				return
			}

			if err := verifySlackSignature(signingSecret,
				r.Header.Get("X-Slack-Request-Timestamp"), r.Header.Get("X-Slack-Signature"), body, time.Now(),
			); err != nil {
				errutil.Handle(ctx, err, "slack request rejected")
				w.WriteHeader(http.StatusUnauthorized)
				return
			}

			r.Body = io.NopCloser(bytes.NewReader(body))
			next.ServeHTTP(w, r)
		})
	}
}

func (s *Server) slackEventHandler(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	body, err := io.ReadAll(r.Body)
	if err != nil {
		errutil.Handle(ctx, goerr.Wrap(err, "failed to read slack event body"), "slack event rejected")
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	event, err := slackevents.ParseEvent(json.RawMessage(body), slackevents.OptionNoVerifyToken())
	if err != nil {
		errutil.Handle(ctx, goerr.Wrap(err, "failed to parse slack event"), "slack event rejected")
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	switch event.Type {
	case slackevents.URLVerification:
		var challenge slackevents.ChallengeResponse
		if err := json.Unmarshal(body, &challenge); err != nil {
			errutil.Handle(ctx, goerr.Wrap(err, "failed to parse url_verification challenge"), "slack event rejected")
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "text/plain")
		w.WriteHeader(http.StatusOK)
		if _, err := w.Write([]byte(challenge.Challenge)); err != nil {
			errutil.Handle(ctx, goerr.Wrap(err, "failed to write challenge response"), "failed to write response")
		}

	case slackevents.CallbackEvent:
		// Slack requires a response within three seconds, so acknowledge
		// first and run everything that touches Firestore, KMS or Slack in
		// the background.
		w.WriteHeader(http.StatusOK)
		async.Dispatch(ctx, func(ctx context.Context) error {
			return s.slackUC.HandleEvent(ctx, &event)
		})

	default:
		w.WriteHeader(http.StatusOK)
	}
}

// slackInteractionHandler receives the interactivity requests of the Slack
// app. Slack sends them as a form with one "payload" field holding JSON. Only
// message shortcuts are handled; the rest are acknowledged and ignored.
func (s *Server) slackInteractionHandler(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	if err := r.ParseForm(); err != nil {
		errutil.Handle(ctx, goerr.Wrap(err, "failed to parse slack interaction form"), "slack interaction rejected")
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	payload := r.PostForm.Get("payload")
	if payload == "" {
		errutil.Handle(ctx, goerr.New("slack interaction has no payload"), "slack interaction rejected")
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	var callback slack.InteractionCallback
	if err := json.Unmarshal([]byte(payload), &callback); err != nil {
		errutil.Handle(ctx, goerr.Wrap(err, "failed to parse slack interaction payload"), "slack interaction rejected")
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	w.WriteHeader(http.StatusOK)
	if callback.Type != slack.InteractionTypeMessageAction {
		return
	}
	shortcut := model.SlackMessageShortcut{
		TeamID:     model.SlackTeamID(callback.Team.ID),
		CallbackID: callback.CallbackID,
		ChannelID:  callback.Channel.ID,
		UserID:     model.SlackUserID(callback.User.ID),
		MessageTS:  callback.MessageTs,
	}
	async.Dispatch(ctx, func(ctx context.Context) error {
		return s.slackUC.HandleMessageShortcut(ctx, shortcut)
	})
}
