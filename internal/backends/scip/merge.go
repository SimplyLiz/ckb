package scip

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"

	"google.golang.org/protobuf/encoding/protowire"
)

const indexFieldExternalSymbols = 3

// MergeIndexes writes one SCIP index at outPath holding the documents and
// external symbols of every index in inputs, for a repo that needs one
// indexer per language. Every reader of the index (loader, incremental
// tracking, streaming) handles a single file with documents from several
// indexers, so merging is the whole of multi-language support on disk.
//
// The inputs must already be repo-relative (see RebaseDocumentPaths). The
// metadata comes from the first input; all of them share the project root.
// A document path seen twice keeps its first copy, and an external symbol
// seen twice keeps its first definition. outPath is replaced atomically, so
// a failure leaves whatever was there before; outPath may be one of inputs.
func MergeIndexes(outPath string, inputs []string) (err error) {
	if len(inputs) == 0 {
		return fmt.Errorf("no indexes to merge")
	}

	tmp, err := os.CreateTemp(filepath.Dir(outPath), ".index-merge-*")
	if err != nil {
		return err
	}
	defer func() {
		if err != nil {
			_ = tmp.Close()
			_ = os.Remove(tmp.Name())
		}
	}()
	out := bufio.NewWriter(tmp)

	seenDocs := make(map[string]bool)
	seenExternal := make(map[string]bool)
	wroteMeta := false

	for _, in := range inputs {
		data, readErr := os.ReadFile(in) // #nosec G304 -- paths from the index plan
		if readErr != nil {
			return readErr
		}
		for b := data; len(b) > 0; {
			num, typ, n := protowire.ConsumeTag(b)
			if n < 0 {
				return fmt.Errorf("%s: invalid field tag", in)
			}
			tag := b[:n]
			b = b[n:]
			size := protowire.ConsumeFieldValue(num, typ, b)
			if size < 0 {
				return fmt.Errorf("%s: malformed field %d", in, num)
			}
			value := b[:size]
			b = b[size:]

			if typ == protowire.BytesType {
				switch num {
				case indexFieldMetadata:
					// Metadata is a singular message: a second copy would merge
					// into the first field by field (tool arguments appended).
					if wroteMeta {
						continue
					}
					wroteMeta = true
				case indexFieldDocument, indexFieldExternalSymbols:
					payload, consumed := protowire.ConsumeBytes(value)
					if consumed < 0 {
						return fmt.Errorf("%s: malformed field %d", in, num)
					}
					// Document.relative_path and SymbolInformation.symbol are
					// both field 1.
					key := firstStringField(payload)
					seen := seenDocs
					if num == indexFieldExternalSymbols {
						seen = seenExternal
					}
					if key != "" {
						if seen[key] {
							continue
						}
						seen[key] = true
					}
				}
			}

			if _, err = out.Write(tag); err != nil {
				return err
			}
			if _, err = out.Write(value); err != nil {
				return err
			}
		}
	}

	if err = out.Flush(); err != nil {
		return err
	}
	// CreateTemp makes the file 0600; an indexer's output is 0644.
	if err = tmp.Chmod(0o644); err != nil {
		return err
	}
	if err = tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), outPath)
}

// firstStringField returns field 1 of a message when it is a length-delimited
// value, without decoding the rest.
func firstStringField(msg []byte) string {
	for b := msg; len(b) > 0; {
		num, typ, n := protowire.ConsumeTag(b)
		if n < 0 {
			return ""
		}
		b = b[n:]
		if num == 1 && typ == protowire.BytesType {
			v, m := protowire.ConsumeBytes(b)
			if m < 0 {
				return ""
			}
			return string(v)
		}
		m := protowire.ConsumeFieldValue(num, typ, b)
		if m < 0 {
			return ""
		}
		b = b[m:]
	}
	return ""
}
