package cli

import (
	"github.com/urfave/cli/v3"
)

var NewJobsForTest = newJobs

func flagNames(flags []cli.Flag) []string {
	var out []string
	for _, f := range flags {
		out = append(out, f.Names()[0])
	}
	return out
}

func ServeFlagNamesForTest() []string    { return flagNames(cmdServe().Flags) }
func ScheduleFlagNamesForTest() []string { return flagNames(cmdSchedule().Flags) }
