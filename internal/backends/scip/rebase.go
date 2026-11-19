package scip

import (
	"bufio"
	"fmt"
	"os"
	"path"
	"path/filepath"

	"google.golang.org/protobuf/encoding/protowire"
	"google.golang.org/protobuf/proto"

	scippb "github.com/sourcegraph/scip/bindings/go/scip"
)

const (
	indexFieldMetadata = 1
	indexFieldDocument = 2
)

// RebaseDocumentPaths rewrites the index at indexPath so its documents are
// relative to projectRoot: prefix is joined onto every document path and the
// metadata's project root is replaced. An indexer that ran in a subdirectory
// records paths relative to that directory, while everything reading the index
// expects them relative to the repo.
//
// The index is streamed field by field, like the loader does, and replaced
// atomically, so a failure leaves the original in place.
func RebaseDocumentPaths(indexPath, prefix, projectRoot string) (err error) {
	info, err := os.Stat(indexPath)
	if err != nil {
		return err
	}
	data, err := os.ReadFile(indexPath) // #nosec G304 -- path from the index plan
	if err != nil {
		return err
	}

	tmp, err := os.CreateTemp(filepath.Dir(indexPath), ".index-rebase-*")
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
	for b := data; len(b) > 0; {
		num, typ, n := protowire.ConsumeTag(b)
		if n < 0 {
			return fmt.Errorf("%s: invalid field tag", indexPath)
		}
		tag := b[:n]
		b = b[n:]

		size := protowire.ConsumeFieldValue(num, typ, b)
		if size < 0 {
			return fmt.Errorf("%s: malformed field %d", indexPath, num)
		}
		value := b[:size]
		b = b[size:]

		if typ == protowire.BytesType && (num == indexFieldMetadata || num == indexFieldDocument) {
			payload, consumed := protowire.ConsumeBytes(value)
			if consumed < 0 {
				return fmt.Errorf("%s: malformed field %d", indexPath, num)
			}
			rewritten, rewriteErr := rebaseField(num, payload, prefix, projectRoot)
			if rewriteErr != nil {
				return fmt.Errorf("%s: %w", indexPath, rewriteErr)
			}
			value = protowire.AppendBytes(nil, rewritten)
		}

		if _, err = out.Write(tag); err != nil {
			return err
		}
		if _, err = out.Write(value); err != nil {
			return err
		}
	}

	if err = out.Flush(); err != nil {
		return err
	}
	if err = tmp.Close(); err != nil {
		return err
	}
	// CreateTemp makes the file private; the index keeps the indexer's mode.
	if err = os.Chmod(tmp.Name(), info.Mode().Perm()); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), indexPath)
}

func rebaseField(num protowire.Number, payload []byte, prefix, projectRoot string) ([]byte, error) {
	if num == indexFieldMetadata {
		var meta scippb.Metadata
		if err := proto.Unmarshal(payload, &meta); err != nil {
			return nil, fmt.Errorf("parsing metadata: %w", err)
		}
		meta.ProjectRoot = "file://" + filepath.ToSlash(projectRoot)
		return proto.Marshal(&meta)
	}

	var doc scippb.Document
	if err := proto.Unmarshal(payload, &doc); err != nil {
		return nil, fmt.Errorf("parsing document: %w", err)
	}
	doc.RelativePath = path.Join(prefix, doc.RelativePath)
	return proto.Marshal(&doc)
}
