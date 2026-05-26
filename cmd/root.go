package cmd

import (
	"fmt"
	"io"
	"log"
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
	logFileFlag = &cli.StringFlag{
		Name:  "log-file",
		Usage: "write log output to file (in addition to stderr)",
	}
	pebbleV2Flag = &cli.BoolFlag{
		Name:  "pebblev2",
		Usage: "run benchmarks against Pebble v2 instead of v1",
	}
)

// sharedFlags returns the flags common to all subcommands.
func sharedFlags() []cli.Flag {
	return []cli.Flag{configFlag, overrideFlag, dataDirFlag, logFileFlag, pebbleV2Flag}
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

	// Apply --pebblev2 flag. Only override the config value when the flag was
	// explicitly provided so a config file can still enable v2 on its own.
	if c.IsSet(pebbleV2Flag.Name) {
		cfg.PebbleV2 = c.Bool(pebbleV2Flag.Name)
	}

	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	return cfg, nil
}

// setupLogFile configures log output to write to both stderr and the given file.
// Returns a cleanup function that must be called to close the file.
func setupLogFile(c *cli.Context) func() {
	path := c.String(logFileFlag.Name)
	if path == "" {
		return func() {}
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		log.Printf("Failed to open log file %s: %v", path, err)
		return func() {}
	}
	log.SetOutput(io.MultiWriter(os.Stderr, f))
	return func() {
		log.SetOutput(os.Stderr)
		f.Close()
	}
}
