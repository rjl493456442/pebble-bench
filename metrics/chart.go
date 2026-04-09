package metrics

import (
	"fmt"
	"strings"
	"time"
)

const (
	chartHeight = 15
	chartWidth  = 60
)

// ChartPoint represents a single data point in a time series chart.
type ChartPoint struct {
	Elapsed time.Duration
	Value   float64
}

// PrintChart renders an ASCII bar chart of values over time.
func PrintChart(title string, points []ChartPoint) {
	if len(points) < 2 {
		return
	}

	values := make([]float64, len(points))
	var maxVal float64
	for i, p := range points {
		values[i] = p.Value
		if p.Value > maxVal {
			maxVal = p.Value
		}
	}
	if maxVal == 0 {
		return
	}

	// Resample to fit chart width
	bars := resample(values, chartWidth)

	// Find max after resampling
	maxBar := 0.0
	for _, v := range bars {
		if v > maxBar {
			maxBar = v
		}
	}
	if maxBar == 0 {
		return
	}

	fmt.Println()
	fmt.Println(title)
	fmt.Println()

	// Render rows top-down
	for row := chartHeight; row >= 1; row-- {
		threshold := maxBar * float64(row) / float64(chartHeight)

		switch row {
		case chartHeight:
			fmt.Printf("  %8.0f |", maxBar)
		case chartHeight/2 + 1:
			fmt.Printf("  %8.0f |", maxBar/2)
		case 1:
			fmt.Printf("  %8.0f |", maxBar/float64(chartHeight))
		default:
			fmt.Printf("           |")
		}

		for _, v := range bars {
			if v >= threshold {
				fmt.Print("█")
			} else {
				fmt.Print(" ")
			}
		}
		fmt.Println()
	}

	// X-axis
	fmt.Printf("           +%s\n", strings.Repeat("─", len(bars)))
	totalDuration := points[len(points)-1].Elapsed
	fmt.Printf("            %-*s%s\n", len(bars)/2, "0s", totalDuration.Round(time.Second))
}

// resample reduces values to the target number of buckets by averaging.
func resample(values []float64, target int) []float64 {
	n := len(values)
	if n <= target {
		return values
	}
	result := make([]float64, target)
	for i := range target {
		lo := float64(i) * float64(n) / float64(target)
		hi := float64(i+1) * float64(n) / float64(target)

		start := int(lo)
		end := int(hi)
		if end > n {
			end = n
		}
		if start == end {
			result[i] = values[start]
			continue
		}
		var sum float64
		for j := start; j < end; j++ {
			sum += values[j]
		}
		result[i] = sum / float64(end-start)
	}
	return result
}
