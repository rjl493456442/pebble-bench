package cmd

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/rjl493456442/pebble-bench/metrics"
	"github.com/urfave/cli/v2"
)

var compareOutputFileFlag = &cli.StringFlag{
	Name:  "output-file",
	Usage: "write comparison to a markdown file",
}

func compareCommand() *cli.Command {
	return &cli.Command{
		Name:      "compare",
		Usage:     "Compare two benchmark results",
		ArgsUsage: "<baseline.json> <current.json>",
		Description: `Load two JSON result files produced by 'run --output json' and print
a side-by-side comparison with percentage differences.

Example:
  pebble-bench compare baseline.json current.json
  pebble-bench compare --output-file diff.md baseline.json current.json`,
		Flags: []cli.Flag{
			compareOutputFileFlag,
		},
		Action: runCompare,
	}
}

func runCompare(c *cli.Context) error {
	if c.NArg() != 2 {
		return fmt.Errorf("exactly two result files required, got %d", c.NArg())
	}

	baseline, err := loadResult(c.Args().Get(0))
	if err != nil {
		return fmt.Errorf("loading baseline: %w", err)
	}
	current, err := loadResult(c.Args().Get(1))
	if err != nil {
		return fmt.Errorf("loading current: %w", err)
	}

	metrics.PrintComparison(baseline, current)

	if outPath := c.String(compareOutputFileFlag.Name); outPath != "" {
		if err := metrics.WriteMultiMarkdown(outPath, []*metrics.Result{baseline, current}); err != nil {
			return fmt.Errorf("writing comparison: %w", err)
		}
		fmt.Printf("\nComparison written to %s\n", outPath)
	}
	return nil
}

func loadResult(path string) (*metrics.Result, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var r metrics.Result
	if err := json.Unmarshal(data, &r); err != nil {
		return nil, err
	}
	return &r, nil
}
