package main

import (
	"fmt"
	"strings"

	"github.com/SimplyLiz/CodeMCP/internal/mcp"
	"github.com/SimplyLiz/CodeMCP/internal/project"
)

// diagnoseIndex checks what building the SCIP index for repoRoot would run
// into, with the same steps triggerReindex takes (language, repo size,
// indexer plan), so the next step it names is the one that works here.
func diagnoseIndex(repoRoot string) mcp.IndexDiagnosis {
	lang, ok := watchDetectLanguage(repoRoot)
	if !ok {
		_, _, all := project.DetectAllLanguages(repoRoot)
		if len(all) > 1 {
			names := make([]string, 0, len(all))
			for _, l := range all {
				names = append(names, string(l))
			}
			return mcp.IndexDiagnosis{
				Problem: fmt.Sprintf("This repo has several languages (%s) and CKB indexes one; "+
					"run 'ckb index --lang <language>' in a terminal with the one to index, "+
					"e.g. 'ckb index --lang %s'. This server loads the index on its next call.",
					strings.Join(names, ", "), names[0]),
			}
		}
		return mcp.IndexDiagnosis{
			Problem: "CKB found no language here that it has a SCIP indexer for, so this stays the " +
				"text-and-git tier. If the code is in a supported language without a manifest, " +
				"run 'ckb index --lang <language>' in a terminal.",
		}
	}

	if n := countSourceFiles(repoRoot, lang); n >= scipLargeRepoThreshold {
		return mcp.IndexDiagnosis{
			Problem: fmt.Sprintf("The repo has %d %s files, more than CKB indexes without being asked "+
				"(%d); run 'ckb index --scip' in a terminal. This server loads the index on its next call.",
				n, project.LanguageDisplayName(lang), scipLargeRepoThreshold),
		}
	}

	manifest := project.FindManifestForLanguage(repoRoot, lang)
	plan, result := buildIndexPlan(repoRoot, lang, manifest, "", resolveIndexPath(repoRoot))
	if plan != nil {
		return mcp.IndexDiagnosis{Ready: true}
	}
	switch result.Outcome {
	case indexOutcomeIndexerMissing, indexOutcomeIndexerRequirementMissing:
		return mcp.IndexDiagnosis{
			Problem:     fmt.Sprintf("Indexing %s needs this first: %s.", project.LanguageDisplayName(lang), result.Message),
			ThenReindex: true,
		}
	default:
		return mcp.IndexDiagnosis{
			Problem:     fmt.Sprintf("Indexing %s cannot start: %s.", project.LanguageDisplayName(lang), result.Message),
			ThenReindex: true,
		}
	}
}
