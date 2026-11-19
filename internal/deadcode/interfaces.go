package deadcode

import (
	"sort"
	"strings"

	bindingscip "github.com/sourcegraph/scip/bindings/go/scip"

	"github.com/SimplyLiz/CodeMCP/internal/backends/scip"
)

// A method called through an interface has no reference of its own: `c.Run()`
// references `Check#Run`, not `alpha#Run`. In SCIP the method reaches the
// interface only when the indexer wrote an implementation relationship, which
// scip-go does for a fraction of its methods, so Go is matched by method set
// as the language does it: a type whose method names include every method of
// the interface implements it. The reachable predicate says what counts as
// alive: for unwired, reached from an entrypoint; for dead code, referenced.

// InterfaceReach answers whether a method is reached through an interface:
// the interface or the method it declares is reachable, and so is the type
// the method belongs to. A popular interface does not wire a type nobody
// builds.
type InterfaceReach struct {
	reachable func(symbol string) bool
	// interface symbol -> the names of the methods it declares
	declared map[string][]string
	// method name -> the interfaces declaring a method of that name
	byName map[string][]string
	// type symbol -> the names of its methods
	methods map[string]map[string]bool
}

func NewInterfaceReach(symbols []*scip.SymbolInformation, reachable func(symbol string) bool) *InterfaceReach {
	r := &InterfaceReach{
		reachable: reachable,
		declared:  make(map[string][]string),
		byName:    make(map[string][]string),
		methods:   make(map[string]map[string]bool),
	}
	interfaces := make(map[string]bool)
	for _, sym := range symbols {
		if bindingscip.SymbolInformation_Kind(sym.Kind) == bindingscip.SymbolInformation_Interface {
			interfaces[sym.Symbol] = true
		}
	}
	for _, sym := range symbols {
		parent, name, ok := SplitMember(sym.Symbol)
		if !ok {
			continue
		}
		switch bindingscip.SymbolInformation_Kind(sym.Kind) {
		case bindingscip.SymbolInformation_MethodSpecification, bindingscip.SymbolInformation_AbstractMethod:
			r.declare(parent, name)
		case bindingscip.SymbolInformation_Method:
			if r.methods[parent] == nil {
				r.methods[parent] = make(map[string]bool)
			}
			r.methods[parent][name] = true
		default:
			// scip-go marks interface methods; an indexer that does not still
			// nests them under a symbol of kind Interface.
			if interfaces[parent] {
				r.declare(parent, name)
			}
		}
	}
	for iface := range r.declared {
		sort.Strings(r.declared[iface])
	}
	for name := range r.byName {
		sort.Strings(r.byName[name])
	}
	return r
}

func (r *InterfaceReach) declare(iface, name string) {
	r.declared[iface] = append(r.declared[iface], name)
	r.byName[name] = append(r.byName[name], iface)
}

// Via returns the interface a method is reached through, or "" when the
// method is not reached that way.
func (r *InterfaceReach) Via(sym *scip.SymbolInformation) string {
	parent, name, ok := SplitMember(sym.Symbol)
	if !ok || !r.reachable(parent) {
		return ""
	}
	// What the indexer wrote down.
	for _, rel := range sym.Relationships {
		if !rel.IsImplementation {
			continue
		}
		iface, _, ok := SplitMember(rel.Symbol)
		if !ok {
			iface = rel.Symbol
		}
		if r.reachable(rel.Symbol) || r.reachable(iface) {
			return DisplayName(iface)
		}
	}
	// What Go's structural typing says.
	for _, iface := range r.byName[name] {
		if !r.satisfies(parent, iface) {
			continue
		}
		if r.reachable(iface) || r.reachable(iface+name+".") {
			return DisplayName(iface)
		}
	}
	return ""
}

// satisfies says whether the type's method names include every method the
// interface declares.
func (r *InterfaceReach) satisfies(typ, iface string) bool {
	have := r.methods[typ]
	for _, name := range r.declared[iface] {
		if !have[name] {
			return false
		}
	}
	return true
}

// SplitMember splits a member symbol `…/Type#name().`, `…/Type#name.` or
// `…/Type#name(+1).` into the type's symbol `…/Type#` and the bare name. A
// type or a nested symbol is not a member.
func SplitMember(symbol string) (parent, name string, ok bool) {
	i := strings.LastIndex(symbol, "#")
	if i < 0 || i == len(symbol)-1 {
		return "", "", false
	}
	rest := symbol[i+1:]
	if strings.ContainsAny(rest, "/`") {
		return "", "", false
	}
	if cut := strings.IndexAny(rest, "(."); cut > 0 {
		rest = rest[:cut]
	}
	if rest == "" {
		return "", "", false
	}
	return symbol[:i+1], rest, true
}

// DisplayName turns `…/pkg`/Check#` into "Check".
func DisplayName(symbol string) string {
	s := strings.TrimSuffix(symbol, "#")
	if i := strings.LastIndexAny(s, "/`"); i >= 0 {
		s = s[i+1:]
	}
	return s
}
