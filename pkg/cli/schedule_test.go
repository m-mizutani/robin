package cli_test

import (
	"context"
	"os"
	"slices"
	"testing"
	"time"

	"github.com/m-mizutani/gt"
	urfavecli "github.com/urfave/cli/v3"

	"github.com/m-mizutani/robin/pkg/cli"
	"github.com/m-mizutani/robin/pkg/cli/config"
)

// scheduleProjectID is a Firestore emulator project of its own: the emulator
// keeps data per project, so the repository tests' jobs are not due here.
const scheduleProjectID = "robin-schedule-test"

func validScheduleArgs() []string {
	return []string{
		"robin", "--log-format", "json", "schedule",
		"--firestore-project-id", scheduleProjectID,
		"--slack-bot-token", "xoxb-token",
		"--anthropic-api-key", "sk-ant-test",
	}
}

func TestSchedule_Validation(t *testing.T) {
	clearServeEnv(t)
	cases := map[string]struct {
		args []string
		want string
	}{
		"memory repository": {
			args: append(validScheduleArgs(), "--repository-backend", "memory"),
			want: "schedule needs the firestore repository that serve uses",
		},
		"no firestore project": {
			args: without(validScheduleArgs(), "--firestore-project-id"),
			want: "--firestore-project-id",
		},
		"no bot token": {
			args: without(validScheduleArgs(), "--slack-bot-token"),
			want: "--slack-bot-token is required",
		},
		"no LLM credential": {
			args: without(validScheduleArgs(), "--anthropic-api-key"),
			want: "--llm-vertex-project-id or --anthropic-api-key is required",
		},
		"zero concurrency": {
			args: append(validScheduleArgs(), "--concurrency", "0"),
			want: "--concurrency must be at least 1",
		},
		"a flag of serve only": {
			args: append(validScheduleArgs(), "--slack-client-id", "client-id"),
			want: "slack-client-id",
		},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			err := cli.Run(context.Background(), c.args, "test")
			gt.Value(t, err).NotNil().Required()
			gt.String(t, err.Error()).Contains(c.want)
		})
	}
}

func names(groups ...[]string) []string {
	var out []string
	for _, g := range groups {
		out = append(out, g...)
	}
	slices.Sort(out)
	return out
}

func flagNames(flags []urfavecli.Flag) []string {
	var out []string
	for _, f := range flags {
		out = append(out, f.Names()[0])
	}
	return out
}

// schedule takes exactly the shared flag groups it needs, defined once in
// pkg/cli/config.
func TestSchedule_FlagGroups(t *testing.T) {
	var (
		file      config.File
		repo      config.Repository
		bot       config.SlackBot
		llm       config.LLM
		scheduler config.Scheduler
	)
	var want []string
	for _, flags := range [][]string{
		flagNames(file.Flags()), flagNames(repo.Flags()), flagNames(bot.Flags()),
		flagNames(llm.Flags()), flagNames(scheduler.Flags()),
	} {
		want = append(want, flags...)
	}
	slices.Sort(want)
	got := cli.ScheduleFlagNamesForTest()
	slices.Sort(got)
	gt.Value(t, got).Equal(want)

	for _, serveOnly := range []string{"slack-client-id", "slack-client-secret", "slack-signing-secret", "slack-team-id", "kms-key-name", "base-url", "no-auth"} {
		gt.False(t, slices.Contains(got, serveOnly))
	}
}

// serve keeps the flags it had before the flag groups were split.
func TestServe_FlagNames(t *testing.T) {
	got := cli.ServeFlagNamesForTest()
	slices.Sort(got)
	want := names([]string{
		"config", "addr", "base-url", "session-ttl",
		"repository-backend", "firestore-project-id", "firestore-database-id",
		"slack-client-id", "slack-client-secret", "slack-signing-secret", "slack-team-id",
		"slack-bot-token",
		"llm-vertex-project-id", "llm-vertex-region", "anthropic-api-key",
		"kms-key-name",
		"google-client-id", "google-client-secret",
		"notion-client-id", "notion-client-secret", "notion-workspace-id", "notion-api-url",
		"github-client-id", "github-client-secret",
		"no-auth",
	})
	gt.Value(t, got).Equal(want)
}

// With no due job, schedule reads Firestore once and exits successfully. It
// uses the emulator, as the repository tests do.
func TestSchedule_RunsAgainstFirestore(t *testing.T) {
	clearServeEnv(t)
	if _, ok := os.LookupEnv("FIRESTORE_EMULATOR_HOST"); !ok {
		t.Setenv("FIRESTORE_EMULATOR_HOST", "127.0.0.1:28615")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	gt.NoError(t, cli.Run(ctx, validScheduleArgs(), "test"))
}
