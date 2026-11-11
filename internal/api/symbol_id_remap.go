package api

import "net/http"

// symbolIDRemappedHeader carries, on a response whose symbol ID came from an
// index at another package version, which ID was used instead.
const symbolIDRemappedHeader = "X-CKB-Symbol-Id-Remapped"

// remapSymbolID maps a SCIP ID from an index built at another commit to the ID
// the loaded index has for the same symbol, as MCP and A2A do (scip-go puts the
// commit hash in the package-version field). When it does, the response says so
// in symbolIDRemappedHeader, as "<old> -> <new>". Call it before anything is
// written to w.
func (s *Server) remapSymbolID(w http.ResponseWriter, id string) string {
	got, remapped := s.engine.ResolveSymbolIDAcrossVersions(id)
	if remapped {
		w.Header().Set(symbolIDRemappedHeader, id+" -> "+got)
	}
	return got
}
