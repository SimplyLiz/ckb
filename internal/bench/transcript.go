package bench

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// maxTranscriptLine bounds the scanner's per-line buffer. Claude Code
// transcript lines can carry large tool_use inputs or tool_result content;
// 64 MiB comfortably covers real transcripts while still bounding memory on a
// corrupt or hostile file.
const maxTranscriptLine = 64 * 1024 * 1024

// rawRecord is one JSONL line of a Claude Code transcript. Only the fields
// bench needs are declared; encoding/json ignores the rest, which is how
// unknown top-level "type" values (mode, bridge-session, system,
// cost-state, ai-title, ...) are tolerated without an explicit allow-list.
type rawRecord struct {
	Type      string      `json:"type"`
	SessionID string      `json:"sessionId"`
	Timestamp string      `json:"timestamp"`
	Cwd       string      `json:"cwd"`
	UUID      string      `json:"uuid"`
	Message   *rawMessage `json:"message"`
}

type rawMessage struct {
	ID      string          `json:"id"`
	Role    string          `json:"role"`
	Usage   *rawUsage       `json:"usage"`
	Content json.RawMessage `json:"content"`
}

type rawUsage struct {
	InputTokens              int              `json:"input_tokens"`
	CacheCreationInputTokens int              `json:"cache_creation_input_tokens"`
	CacheReadInputTokens     int              `json:"cache_read_input_tokens"`
	OutputTokens             int              `json:"output_tokens"`
	OutputTokensDetails      *rawTokenDetails `json:"output_tokens_details"`
}

type rawTokenDetails struct {
	ThinkingTokens int `json:"thinking_tokens"`
}

type rawBlock struct {
	Type  string          `json:"type"`
	ID    string          `json:"id"`
	Name  string          `json:"name"`
	Input json.RawMessage `json:"input"`
}

type rawToolInput struct {
	FilePath string `json:"file_path"`
}

// LocateTranscript finds the JSONL transcript for sessionID under
// ~/.claude/projects. It first tries the exact slug derived from cwd
// (absolute path with every "/" replaced by "-"), then falls back to
// globbing every project directory for "<sessionID>.jsonl" — the transcript
// can live under a different project dir than the caller's current cwd if
// the session moved directories, or cwd is unknown.
func LocateTranscript(sessionID, cwd string) (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolving home directory: %w", err)
	}
	return locateTranscriptIn(filepath.Join(home, ".claude", "projects"), sessionID, cwd)
}

// SlugifyCwd converts an absolute working directory into the directory name
// Claude Code uses under ~/.claude/projects: every "/" becomes "-".
func SlugifyCwd(cwd string) string {
	return strings.ReplaceAll(cwd, "/", "-")
}

func locateTranscriptIn(projectsDir, sessionID, cwd string) (string, error) {
	if sessionID == "" {
		return "", fmt.Errorf("empty session id")
	}
	if cwd != "" {
		candidate := filepath.Join(projectsDir, SlugifyCwd(cwd), sessionID+".jsonl")
		if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
			return candidate, nil
		}
	}

	matches, err := filepath.Glob(filepath.Join(projectsDir, "*", sessionID+".jsonl"))
	if err != nil {
		return "", fmt.Errorf("globbing for transcript: %w", err)
	}
	if len(matches) == 0 {
		return "", fmt.Errorf("no transcript found for session %q under %s", sessionID, projectsDir)
	}
	// Deterministic choice when more than one project dir happens to have a
	// same-named file (should not happen in practice: session ids are
	// UUIDs) or across repeated test fixtures.
	sort.Strings(matches)
	return matches[0], nil
}

// ParseTranscriptFile opens and parses the transcript at path.
func ParseTranscriptFile(path string) (*SessionRecord, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("opening transcript %s: %w", path, err)
	}
	defer f.Close()
	return ParseTranscript(f)
}

// ParseTranscript streams a Claude Code JSONL transcript from r and builds a
// SessionRecord from its client-side (transcript) fields only; the Ledger
// field is left zero-valued (Calls: -1, see BuildSessionRecord).
//
// Dedupe: a streamed assistant message is split across several JSONL lines
// that share message.id, each carrying a disjoint slice of the final
// content array (verified against real transcripts: content blocks never
// repeat within a message.id group, and usage is identical - the final
// totals - on every line of the group). So turns and token usage are
// counted once per message.id (falling back to the per-line "uuid" when a
// message has no id, which then makes every line its own group), while
// content blocks (tool_use, text, ...) are processed on every line, since
// each line contributes new blocks. tool_use blocks are additionally
// deduped by their own "id" as a defensive measure against a truly
// duplicated line.
//
// Malformed lines (invalid JSON) are skipped and counted in Notes rather
// than failing the parse.
func ParseTranscript(r io.Reader) (*SessionRecord, error) {
	rec := &SessionRecord{
		ToolCalls: map[string]int{},
		CKBTools:  map[string]int{},
		Ledger:    LedgerSummary{Calls: -1},
	}

	seenGroups := map[string]bool{}
	seenToolUseIDs := map[string]bool{}
	fileSet := map[string]bool{}

	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, 64*1024), maxTranscriptLine)

	var (
		malformed  int
		lineNo     int
		haveStart  bool
		haveEnd    bool
		sawSession bool
	)

	for scanner.Scan() {
		lineNo++
		line := scanner.Bytes()
		trimmed := strings.TrimSpace(string(line))
		if trimmed == "" {
			continue
		}

		var rr rawRecord
		if err := json.Unmarshal([]byte(trimmed), &rr); err != nil {
			malformed++
			continue
		}

		if rr.SessionID != "" && !sawSession {
			rec.SessionID = rr.SessionID
			sawSession = true
		}
		if rr.Cwd != "" && rec.Cwd == "" {
			rec.Cwd = rr.Cwd
		}
		if ts, ok := parseTimestamp(rr.Timestamp); ok {
			if !haveStart || ts.Before(rec.Start) {
				rec.Start = ts
				haveStart = true
			}
			if !haveEnd || ts.After(rec.End) {
				rec.End = ts
				haveEnd = true
			}
		}

		if rr.Type != "assistant" {
			continue
		}
		if rr.Message == nil {
			malformed++
			continue
		}

		groupKey := rr.Message.ID
		if groupKey == "" {
			groupKey = rr.UUID
		}
		if groupKey != "" && !seenGroups[groupKey] {
			seenGroups[groupKey] = true
			rec.Turns++
			if rr.Message.Usage != nil {
				u := rr.Message.Usage
				rec.Tokens.Input += u.InputTokens
				rec.Tokens.CacheCreation += u.CacheCreationInputTokens
				rec.Tokens.CacheRead += u.CacheReadInputTokens
				rec.Tokens.Output += u.OutputTokens
				if u.OutputTokensDetails != nil {
					rec.Tokens.Thinking += u.OutputTokensDetails.ThinkingTokens
				}
			}
		} else if groupKey == "" {
			// No message id and no record uuid: cannot dedupe or safely
			// skip re-counting. Treat conservatively as its own turn.
			rec.Turns++
			if rr.Message.Usage != nil {
				u := rr.Message.Usage
				rec.Tokens.Input += u.InputTokens
				rec.Tokens.CacheCreation += u.CacheCreationInputTokens
				rec.Tokens.CacheRead += u.CacheReadInputTokens
				rec.Tokens.Output += u.OutputTokens
				if u.OutputTokensDetails != nil {
					rec.Tokens.Thinking += u.OutputTokensDetails.ThinkingTokens
				}
			}
		}

		blocks, ok := parseBlocks(rr.Message.Content)
		if !ok {
			continue
		}
		for _, b := range blocks {
			if b.Type != "tool_use" {
				continue
			}
			if b.ID != "" {
				if seenToolUseIDs[b.ID] {
					continue
				}
				seenToolUseIDs[b.ID] = true
			}
			name := b.Name
			if name == "" {
				continue
			}
			rec.ToolCalls[name]++

			switch name {
			case "Read":
				rec.AgentFileReads++
				if fp := extractFilePath(b.Input); fp != "" {
					fileSet[fp] = true
				}
			case "Grep", "Glob":
				rec.AgentSearches++
			case "Bash":
				rec.BashCalls++
			}

			if strings.HasPrefix(name, "mcp__ckb__") {
				rec.CKBCalls++
				rec.CKBTools[name]++
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("reading transcript: %w", err)
	}

	// Total is Input + CacheCreation + CacheRead + Output. Thinking tokens
	// are a sub-breakdown of Output (per Anthropic's usage reporting, a
	// thinking block's tokens are already counted in output_tokens), so
	// Thinking is not added again here - doing so would double count it.
	rec.Tokens.Total = rec.Tokens.Input + rec.Tokens.CacheCreation + rec.Tokens.CacheRead + rec.Tokens.Output

	rec.DistinctFiles = len(fileSet)
	if haveStart && haveEnd {
		rec.Duration = rec.End.Sub(rec.Start)
	}
	if malformed > 0 {
		rec.Notes = append(rec.Notes, fmt.Sprintf("%d malformed line(s) skipped", malformed))
	}
	if len(rec.ToolCalls) == 0 {
		rec.ToolCalls = nil
	}
	if len(rec.CKBTools) == 0 {
		rec.CKBTools = nil
	}

	return rec, nil
}

func parseTimestamp(s string) (time.Time, bool) {
	if s == "" {
		return time.Time{}, false
	}
	if t, err := time.Parse(time.RFC3339Nano, s); err == nil {
		return t, true
	}
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t, true
	}
	return time.Time{}, false
}

// parseBlocks decodes message.content as an array of content blocks. Some
// records (plain user turns) carry content as a bare string rather than an
// array; that is not an error, just nothing to extract, signalled by ok=false.
func parseBlocks(raw json.RawMessage) (blocks []rawBlock, ok bool) {
	if len(raw) == 0 {
		return nil, false
	}
	if err := json.Unmarshal(raw, &blocks); err != nil {
		return nil, false
	}
	return blocks, true
}

func extractFilePath(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var in rawToolInput
	if err := json.Unmarshal(raw, &in); err != nil {
		return ""
	}
	return in.FilePath
}
