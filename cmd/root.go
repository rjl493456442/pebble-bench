package cmd

import (
	"fmt"
	"os"
	"strings"

	"github.com/rjl493456442/pebble-bench/config"
	"github.com/urfave/cli/v2"
)

// Shared flags used by multiple subcommands.
var (
	configFlag = &cli.StringFlag{
		Name:    "config",
		Aliases: []string{"c"},
		Usage:   "path to YAML config file",
	}
	overrideFlag = &cli.StringSliceFlag{
		Name:  "override",
		Usage: "config overrides in key=value format (repeatable). Supported keys: " + strings.Join(config.ListOverrideKeys(), ", "),
	}
	dataDirFlag = &cli.StringFlag{
		Name:  "data-dir",
		Usage: "override data directory",
	}
)

// sharedFlags returns the flags common to all subcommands.
func sharedFlags() []cli.Flag {
	return []cli.Flag{configFlag, overrideFlag, dataDirFlag}
}

// App returns the CLI application.
func App() *cli.App {
	return &cli.App{
		Name:  "pebble-bench",
		Usage: "Benchmarking tool for Pebble key-value store",
		Description: `pebble-bench is a benchmarking tool for measuring Pebble performance
under various workloads. It supports scan, random read, write,
and mixed read/write scenarios.

Use 'init' to populate a dataset, then 'run' to execute benchmarks.
Use 'compare' to diff two result files side by side.
Configuration can be provided via YAML files and overridden with --override flags.`,
		Commands: []*cli.Command{
			initCommand(),
			runCommand(),
			compareCommand(),
		},
	}
}

// Execute runs the CLI application.
func Execute() {
	app := App()
	if err := app.Run(os.Args); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

// loadConfig loads the config from file (if specified) and applies overrides.
func loadConfig(c *cli.Context) (*config.BenchConfig, error) {
	var cfg *config.BenchConfig

	if configFile := c.String(configFlag.Name); configFile != "" {
		var err error
		cfg, err = config.LoadConfig(configFile)
		if err != nil {
			return nil, err
		}
	} else {
		cfg = config.DefaultConfig()
	}

	// Apply --override flags
	if overrides := c.StringSlice(overrideFlag.Name); len(overrides) > 0 {
		if err := config.ApplyOverrides(cfg, overrides); err != nil {
			return nil, err
		}
	}

	// Apply --data-dir flag
	if dataDir := c.String(dataDirFlag.Name); dataDir != "" {
		cfg.DataDir = dataDir
	}

	return cfg, nil
}
