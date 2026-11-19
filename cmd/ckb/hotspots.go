package main

import (
	"fmt"
	"os"
	"time"

	"github.com/spf13/cobra"

	"github.com/SimplyLiz/CodeMCP/internal/pathclass"
	"github.com/SimplyLiz/CodeMCP/internal/query"
)

var (
	hotspotsFormat    string
	hotspotsScope     string
	hotspotsLimit     int
	hotspotsTimeStart string
	hotspotsTimeEnd   string
)

var hotspotsCmd = &cobra.Command{
	Use:   "hotspots",
	Short: "Find files that deserve attention based on churn and coupling",
	Long: `Find hotspot files based on churn, coupling, and recency.

Highlights volatile areas that may need review or refactoring.
Default time window is 30 days.

Examples:
  ckb hotspots
  ckb hotspots --scope=internal/api
  ckb hotspots --limit=50
  ckb hotspots --start=2024-01-01 --end=2024-06-30
  ckb hotspots --format=human`,
	Run: runHotspots,
}

func init() {
	hotspotsCmd.Flags().StringVar(&hotspotsFormat, "format", "human", "Output format (human, json)")
	hotspotsCmd.Flags().StringVar(&hotspotsScope, "scope", "", "Module path to focus on")
	hotspotsCmd.Flags().IntVar(&hotspotsLimit, "limit", 20, "Maximum hotspots to return (max 50)")
	hotspotsCmd.Flags().StringVar(&hotspotsTimeStart, "start", "", "Start date (ISO8601 or YYYY-MM-DD)")
	hotspotsCmd.Flags().StringVar(&hotspotsTimeEnd, "end", "", "End date (ISO8601 or YYYY-MM-DD)")
	rootCmd.AddCommand(hotspotsCmd)
}

func runHotspots(cmd *cobra.Command, args []string) {
	start := time.Now()
	logger := newLogger(hotspotsFormat)

	repoRoot := mustGetRepoRoot()
	engine := mustGetEngine(repoRoot, logger)
	ctx := newContext()

	opts := query.GetHotspotsOptions{
		Scope: hotspotsScope,
		Limit: hotspotsLimit,
	}

	if hotspotsTimeStart != "" || hotspotsTimeEnd != "" {
		opts.TimeWindow = &query.TimeWindowSelector{
			Start: hotspotsTimeStart,
			End:   hotspotsTimeEnd,
		}
	}

	response, err := engine.GetHotspots(ctx, opts)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error getting hotspots: %v\n", err)
		os.Exit(1)
	}

	cliResponse := convertHotspotsResponse(response)

	output, err := FormatResponse(cliResponse, OutputFormat(hotspotsFormat))
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error formatting output: %v\n", err)
		os.Exit(1)
	}

	fmt.Println(output)

	logger.Debug("Hotspots query completed",
		"count", len(response.Hotspots),
		"duration", time.Since(start).Milliseconds(),
	)
}

// HotspotsResponseCLI contains hotspots list for CLI output
type HotspotsResponseCLI struct {
	Hotspots    []HotspotCLI `json:"hotspots"`
	TotalCount  int          `json:"totalCount"`
	TimeWindow  string       `json:"timeWindow"`
	Confidence  float64      `json:"confidence"`
	Limitations []string     `json:"limitations,omitempty"`
	// LeftOut counts the files in generated or data paths kept out of the ranking.
	LeftOut    *pathclass.Tally `json:"leftOut,omitempty"`
	Provenance *ProvenanceCLI   `json:"provenance,omitempty"`
}

type HotspotCLI struct {
	FilePath  string          `json:"filePath"`
	Role      string          `json:"role,omitempty"`
	Language  string          `json:"language,omitempty"`
	Churn     HotspotChurnCLI `json:"churn"`
	Recency   string          `json:"recency"`
	RiskLevel string          `json:"riskLevel"`
	Score     float64         `json:"score"`
}

type HotspotChurnCLI struct {
	ChangeCount    int     `json:"changeCount"`
	AuthorCount    int     `json:"authorCount"`
	AverageChanges float64 `json:"averageChanges"`
	Score          float64 `json:"score"`
}

func convertHotspotsResponse(resp *query.GetHotspotsResponse) *HotspotsResponseCLI {
	hotspots := make([]HotspotCLI, 0, len(resp.Hotspots))
	for _, h := range resp.Hotspots {
		hotspot := HotspotCLI{
			FilePath: h.FilePath,
			Role:     h.Role,
			Language: h.Language,
			Churn: HotspotChurnCLI{
				ChangeCount:    h.Churn.ChangeCount,
				AuthorCount:    h.Churn.AuthorCount,
				AverageChanges: h.Churn.AverageChanges,
				Score:          h.Churn.Score,
			},
			Recency:   h.Recency,
			RiskLevel: h.RiskLevel,
		}
		if h.Ranking != nil {
			hotspot.Score = h.Ranking.Score
		}
		hotspots = append(hotspots, hotspot)
	}

	result := &HotspotsResponseCLI{
		Hotspots:    hotspots,
		TotalCount:  resp.TotalCount,
		TimeWindow:  resp.TimeWindow,
		Confidence:  resp.Confidence,
		Limitations: resp.Limitations,
		LeftOut:     resp.LeftOut,
	}

	if resp.Provenance != nil {
		result.Provenance = &ProvenanceCLI{
			RepoStateId:     resp.Provenance.RepoStateId,
			RepoStateDirty:  resp.Provenance.RepoStateDirty,
			QueryDurationMs: resp.Provenance.QueryDurationMs,
		}
	}

	return result
}
