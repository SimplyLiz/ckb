package main

import (
	"fmt"
	"strings"

	"github.com/SimplyLiz/CodeMCP/internal/mcp"
	"github.com/SimplyLiz/CodeMCP/internal/project"
)

// diagnoseIndex checks what building the SCIP index for repoRoot would run
// into, with the same steps triggerReindex takes (languages, size per
// language, indexer plans), so the next step it names is the one that works
// here. One language that can be indexed is enough for Ready; the ones that
// can't are named in Note.
func diagnoseIndex(repoRoot string) mcp.IndexDiagnosis {
	targets, ok := indexedTargets(repoRoot)
	if !ok {
		return mcp.IndexDiagnosis{
			Problem: "CKB found no language here that it has a SCIP indexer for, so this stays the " +
				"text-and-git tier. If the code is in a supported language without a manifest, " +
				"run 'ckb index --lang <language>' in a terminal.",
		}
	}

	lp := planLanguages(repoRoot, targets, "", resolveIndexPath(repoRoot), false)
	if len(lp.Plans) > 0 {
		d := mcp.IndexDiagnosis{Ready: true}
		if len(lp.Skipped) > 0 {
			d.Note = "The index will leave out " + skippedNote(lp.Skipped) + "."
		}
		return d
	}

	if lp.LargeOnly {
		if len(targets) == 1 {
			n := lp.Skipped[0].Files
			return mcp.IndexDiagnosis{
				Problem: fmt.Sprintf("The repo has %d %s files, more than CKB indexes without being asked "+
					"(%d); run 'ckb index --scip' in a terminal. This server loads the index on its next call.",
					n, project.LanguageDisplayName(targets[0].Lang), scipLargeRepoThreshold),
			}
		}
		return mcp.IndexDiagnosis{
			Problem: "Every language here is over the size CKB indexes without being asked (" +
				skippedNote(lp.Skipped) + "); run 'ckb index --scip' in a terminal. " +
				"This server loads the index on its next call.",
		}
	}

	if len(targets) == 1 {
		lang, res := targets[0].Lang, lp.Failures[0]
		switch res.Outcome {
		case indexOutcomeIndexerMissing, indexOutcomeIndexerRequirementMissing:
			return mcp.IndexDiagnosis{
				Problem:     fmt.Sprintf("Indexing %s needs this first: %s.", project.LanguageDisplayName(lang), res.Message),
				ThenReindex: true,
			}
		default:
			return mcp.IndexDiagnosis{
				Problem:     fmt.Sprintf("Indexing %s cannot start: %s.", project.LanguageDisplayName(lang), res.Message),
				ThenReindex: true,
			}
		}
	}

	// Several languages, none indexable yet: any one fixed is enough.
	return mcp.IndexDiagnosis{
		Problem: fmt.Sprintf("This repo has %s; indexing any of them needs this first: %s.",
			languageNames(targetLanguages(targets)), strings.TrimSpace(skippedNote(lp.Skipped))),
		ThenReindex: true,
	}
}
