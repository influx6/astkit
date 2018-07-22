package sudo

import (
	"fmt"
	"go/ast"
	"go/types"
)

// variables
var (
	// Name applies here
	Name  string
	Creed int = 10
	Fall      = 1000
)

// Functioner interface
type Functioner interface {
	IsAsync() (bool, error)
	IsVariadic() bool
	Node() *ast.FuncDecl
	Rewrite(string, int)
	Params(v string) []string
	Results() []FunctionResult
}

type FunctionResult struct {
	Field string
	Day   int
}

func (f FunctionResult) hello() {
	var name string
	fmt.Printf("Name : %s", name)
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

func (f Function) hello(n string) {
	bu := []string{"sasd", n}
	fmt.Printf("List : %s", bu)
}

func Hello(n string) {
	bu := map[string]string{"sasd": n}
	fmt.Printf("Map : %s", bu)
}
