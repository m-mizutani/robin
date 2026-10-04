package config_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/m-mizutani/gt"

	"github.com/m-mizutani/robin/pkg/cli/config"
	"github.com/m-mizutani/robin/pkg/domain/model"
)

func loadSettings(t *testing.T, args ...string) (*config.Settings, error) {
	t.Helper()
	var f config.File
	parse(t, f.Flags(), args...)
	return f.Load(context.Background())
}

// writeTOML writes content to a file in a temporary directory.
func writeTOML(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "robin.toml")
	gt.NoError(t, os.WriteFile(path, []byte(content), 0o600)).Required()
	return path
}

func TestFile_Defaults(t *testing.T) {
	unsetEnv(t, "ROBIN_CONFIG")

	s, err := loadSettings(t)
	gt.NoError(t, err).Required()
	gt.Equal(t, *s, config.Settings{
		Provider:   "claude",
		Model:      "claude-sonnet-5-5",
		Rate:       model.Rate{Input: 2000, Output: 10000, CacheRead: 200, CacheWrite: 2500},
		Budget:     2_000_000_000,
		SessionTTL: 720 * time.Hour,
	})
}

func TestFile_Full(t *testing.T) {
	s, err := loadSettings(t, "--config", "testdata/full.toml")
	gt.NoError(t, err).Required()
	gt.Equal(t, *s, config.Settings{
		Provider:   "claude",
		Model:      "claude-opus-5-5",
		Rate:       model.Rate{Input: 5000, Output: 25000, CacheRead: 500, CacheWrite: 6250},
		Budget:     3_500_000_000,
		SessionTTL: 168 * time.Hour,
	})
}

func TestFile_PartialTables(t *testing.T) {
	s, err := loadSettings(t, "--config", writeTOML(t, "[agent]\nbudget_usd = 1.5\n"))
	gt.NoError(t, err).Required()
	gt.String(t, s.Model).Equal("claude-sonnet-5-5")
	gt.Value(t, s.Budget).Equal(model.NanoUSD(1_500_000_000))
	gt.Value(t, s.SessionTTL).Equal(720 * time.Hour)

	s, err = loadSettings(t, "--config", writeTOML(t, `[llm]
model = "claude-sonnet-5-5"
input_usd_per_mtok = 3.0
output_usd_per_mtok = 15.0
cache_read_usd_per_mtok = 0.3
cache_write_usd_per_mtok = 3.75
`))
	gt.NoError(t, err).Required()
	gt.String(t, s.Provider).Equal("claude")
	gt.Value(t, s.Rate).Equal(model.Rate{Input: 3000, Output: 15000, CacheRead: 300, CacheWrite: 3750})
	gt.Value(t, s.Budget).Equal(model.NanoUSD(2_000_000_000))

	s, err = loadSettings(t, "--config", writeTOML(t, "[agent]\nsession_ttl = \"24h\"\n"))
	gt.NoError(t, err).Required()
	gt.Value(t, s.SessionTTL).Equal(24 * time.Hour)
}

func TestFile_Errors(t *testing.T) {
	prices := "input_usd_per_mtok = 2.0\noutput_usd_per_mtok = 10.0\ncache_read_usd_per_mtok = 0.2\ncache_write_usd_per_mtok = 2.5\n"
	cases := map[string]string{
		"model without prices":     "[llm]\nmodel = \"claude-sonnet-5-5\"\n",
		"model with some prices":   "[llm]\nmodel = \"m\"\ninput_usd_per_mtok = 2.0\n",
		"provider without model":   "[llm]\nprovider = \"claude\"\n" + prices,
		"prices without model":     "[llm]\n" + prices,
		"another provider":         "[llm]\nprovider = \"gemini\"\nmodel = \"gemini-x\"\n" + prices,
		"zero input price":         "[llm]\nmodel = \"m\"\ninput_usd_per_mtok = 0.0\noutput_usd_per_mtok = 10.0\ncache_read_usd_per_mtok = 0.2\ncache_write_usd_per_mtok = 2.5\n",
		"negative output price":    "[llm]\nmodel = \"m\"\ninput_usd_per_mtok = 2.0\noutput_usd_per_mtok = -1.0\ncache_read_usd_per_mtok = 0.2\ncache_write_usd_per_mtok = 2.5\n",
		"negative cache price":     "[llm]\nmodel = \"m\"\ninput_usd_per_mtok = 2.0\noutput_usd_per_mtok = 10.0\ncache_read_usd_per_mtok = -0.2\ncache_write_usd_per_mtok = 2.5\n",
		"zero budget":              "[agent]\nbudget_usd = 0.0\n",
		"negative budget":          "[agent]\nbudget_usd = -1.0\n",
		"zero session ttl":         "[agent]\nsession_ttl = \"0s\"\n",
		"session ttl not duration": "[agent]\nsession_ttl = \"30 days\"\n",
		"unknown key":              "[agent]\nmax_llm_calls = 3\n",
		"unknown table":            "[server]\naddr = \":8080\"\n",
		"syntax error":             "[agent\nbudget_usd = 2.0\n",
		"wrong type":               "[agent]\nbudget_usd = \"two\"\n",
	}
	for name, content := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := loadSettings(t, "--config", writeTOML(t, content))
			gt.Error(t, err)
		})
	}

	t.Run("missing file", func(t *testing.T) {
		_, err := loadSettings(t, "--config", filepath.Join(t.TempDir(), "none.toml"))
		gt.Error(t, err)
	})
}
