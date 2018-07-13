package sudo

import (
	"go/ast"
	"go/types"
)

var (
	Name  string
	Creed int = 10
	Fall      = 1000
)

// Functioner interface
type Functioner interface {
	IsAsync() (bool, error)
	IsVariadic() bool
	Node() *ast.FuncDecl
	Rewrite()
	Params() []string
	Results() []FunctionResult
}

type FunctionResult struct {
	Field string
	Day   int
}

type Function struct {
	id        string
	path      string
	name      string
	kind      types.Type
	node      *ast.FuncDecl
	exported  bool
	runtime   bool
	processed bool
	async     bool
	imports   map[string]string
	omit      bool
	params    []string
	variadic  bool
	rename    string
}
