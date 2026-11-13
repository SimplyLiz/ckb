package query

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/SimplyLiz/CodeMCP/internal/breaking"
	"github.com/SimplyLiz/CodeMCP/internal/diff"
)

// CompareAPIOptions configures API comparison
type CompareAPIOptions struct {
	BaseRef       string   `json:"baseRef"`       // Git ref for base version (default: HEAD~1)
	TargetRef     string   `json:"targetRef"`     // Git ref for target version (default: HEAD)
	Scope         []string `json:"scope"`         // Limit to specific packages/paths
	IncludeMinor  bool     `json:"includeMinor"`  // Include non-breaking changes in output
	IgnorePrivate bool     `json:"ignorePrivate"` // Only compare exported symbols (default: true)
}

// CompareAPIResponse contains the API comparison result
type CompareAPIResponse struct {
	BaseRef      string            `json:"baseRef"`
	TargetRef    string            `json:"targetRef"`
	Changes      []APIChangeItem   `json:"changes"`
	Summary      *APIChangeSummary `json:"summary"`
	SemverAdvice string            `json:"semverAdvice,omitempty"`
	// NotCompared are files where a public declaration changed in a language whose
	// declarations aren't compared (Python, Java, ...): what changed there is unknown.
	NotCompared []string    `json:"notCompared,omitempty"`
	Provenance  *Provenance `json:"provenance,omitempty"`
}

// APIChangeItem represents a single API change
type APIChangeItem struct {
	Kind         string `json:"kind"`
	Severity     string `json:"severity"`
	SymbolName   string `json:"symbolName"`
	SymbolKind   string `json:"symbolKind"`
	Package      string `json:"package"`
	FilePath     string `json:"filePath"`
	LineNumber   int    `json:"lineNumber,omitempty"`
	Description  string `json:"description"`
	OldValue     string `json:"oldValue,omitempty"`
	NewValue     string `json:"newValue,omitempty"`
	Suggestion   string `json:"suggestion,omitempty"`
	AffectsUsers bool   `json:"affectsUsers"`
}

// APIChangeSummary provides an overview of API changes
type APIChangeSummary struct {
	TotalChanges    int            `json:"totalChanges"`
	BreakingChanges int            `json:"breakingChanges"`
	Warnings        int            `json:"warnings"`
	Additions       int            `json:"additions"`
	ByKind          map[string]int `json:"byKind"`
	ByPackage       map[string]int `json:"byPackage,omitempty"`
}

// CompareAPI compares the declarations a diff changed, from BaseRef's fork point to
// the working tree: the change set's declaration comparison (Go, TypeScript and
// JavaScript, Rust, Python) says which public declarations changed their signature or are
// gone. Declarations of other languages can't be compared; the files they changed in
// are NotCompared, so an empty Changes doesn't read as "nothing broke".
//
// Only the checked-out state can be the target: the index and the files read are the
// working tree's. Another TargetRef is an error rather than a silent HEAD.
func (e *Engine) CompareAPI(ctx context.Context, opts CompareAPIOptions) (*CompareAPIResponse, error) {
	startTime := time.Now()
	if opts.BaseRef == "" {
		opts.BaseRef = "HEAD~1"
	}
	if opts.TargetRef == "" {
		opts.TargetRef = "HEAD"
	}
	if opts.TargetRef != "HEAD" {
		return nil, fmt.Errorf("compareAPI compares against the working tree; check out %s and compare with targetRef HEAD", opts.TargetRef)
	}

	repoState, _ := e.GetRepoState(ctx, "fast")
	cs, err := e.AnalyzeChangeSet(ctx, AnalyzeChangeSetOptions{BaseBranch: opts.BaseRef, TransitiveDepth: 1})
	if err != nil {
		return nil, err
	}
	changes, notCompared := apiChangesFromContracts(cs.Contracts, opts.Scope, opts.IncludeMinor, opts.IgnorePrivate)

	completeness := CompletenessInfo{Score: 0.8, Reason: "declaration-diff"}
	if len(notCompared) > 0 {
		completeness = CompletenessInfo{Score: 0.5, Reason: "declarations-not-compared"}
	}
	response := &CompareAPIResponse{
		BaseRef:     opts.BaseRef,
		TargetRef:   opts.TargetRef,
		Changes:     make([]APIChangeItem, 0, len(changes)),
		NotCompared: notCompared,
		Provenance:  e.buildProvenance(repoState, "fast", startTime, nil, completeness),
	}
	summary, advice := breaking.Summarize(changes)
	response.SemverAdvice = advice
	for _, change := range changes {
		response.Changes = append(response.Changes, APIChangeItem{
			Kind:         string(change.Kind),
			Severity:     string(change.Severity),
			SymbolName:   change.SymbolName,
			FilePath:     change.FilePath,
			Description:  change.Description,
			OldValue:     change.OldValue,
			NewValue:     change.NewValue,
			AffectsUsers: change.AffectsUsers,
		})
	}
	if summary != nil {
		response.Summary = &APIChangeSummary{
			TotalChanges:    summary.TotalChanges,
			BreakingChanges: summary.BreakingChanges,
			Warnings:        summary.Warnings,
			Additions:       summary.Additions,
			ByKind:          summary.ByKind,
			ByPackage:       summary.ByPackage,
		}
	}
	return response, nil
}

// apiChangesFromContracts turns the change set's contract signals into API changes.
// A changed signature or a removed declaration of a public symbol is breaking; a
// compatible one only with includeMinor. A public declaration that couldn't be read
// ("possible-contract-change") is no change: its file is returned as not compared.
// Test files aren't API, and with ignorePrivate neither are the symbols whose name
// makes them private.
func apiChangesFromContracts(contracts []ContractSignal, scope []string, includeMinor, ignorePrivate bool) ([]breaking.APIChange, []string) {
	var changes []breaking.APIChange
	notCompared := make(map[string]bool)
	for _, c := range contracts {
		if isTestFilePath(c.File) || !inAPIScope(c.File, scope) || ignorePrivate && privateByName(c.Symbol, c.File) {
			continue
		}
		change := breaking.APIChange{
			SymbolName:   c.Symbol,
			FilePath:     c.File,
			OldValue:     c.Before,
			NewValue:     c.After,
			AffectsUsers: c.ConsumerCount > 0,
		}
		switch c.Kind {
		case "signature-changed":
			change.Kind, change.Severity = breaking.ChangeSignatureChanged, breaking.SeverityBreaking
			change.Description = fmt.Sprintf("Signature of '%s' changed", c.Symbol)
		case "definition-removed":
			change.Kind, change.Severity = breaking.ChangeRemoved, breaking.SeverityBreaking
			change.Description = fmt.Sprintf("'%s' was removed or renamed", c.Symbol)
		case "compatible-signature-change":
			if !includeMinor {
				continue
			}
			change.Kind, change.Severity = breaking.ChangeSignatureChanged, breaking.SeverityNonBreaking
			change.Description = fmt.Sprintf("Signature of '%s' changed; existing uses should still fit", c.Symbol)
		case "possible-contract-change":
			notCompared[c.File] = true
			continue
		default:
			continue
		}
		if c.ConsumerCount > 0 {
			change.Description += fmt.Sprintf("; %d file(s) still use the old form", c.ConsumerCount)
		}
		changes = append(changes, change)
	}
	files := make([]string, 0, len(notCompared))
	for f := range notCompared {
		files = append(files, f)
	}
	sort.Strings(files)
	return changes, files
}

// privateByName says whether a symbol is private in a language whose names alone
// say so: Go's lowercase, Python's leading underscore (a dunder such as __init__ is
// public). Its signature isn't API, though a use left behind is still a bug, which
// the review's breaking check reports. Elsewhere the declaration decides.
func privateByName(symbol, file string) bool {
	name := leafName(symbol)
	if name == "" {
		return false
	}
	switch diff.DeclarationLanguage(file) {
	case "go":
		return !isExportedSymbol(name, "", "go")
	case "python":
		return strings.HasPrefix(name, "_") && !(strings.HasPrefix(name, "__") && strings.HasSuffix(name, "__"))
	}
	return false
}

func inAPIScope(file string, scope []string) bool {
	if len(scope) == 0 {
		return true
	}
	for _, s := range scope {
		if strings.HasPrefix(file, strings.TrimSuffix(s, "/")) {
			return true
		}
	}
	return false
}
