package sudo

import (
	"fmt"
	"go/ast"
	tokens "go/token"
	"go/types"

	"github.com/gokit/astkit/testbed/sudo/api"
)

const roook = "Roock"

const (
	vood = "wicket"
	day  = 230
)

var _ string = "Rico"

var ui int = 300

var fu = func(string) {}

// variables
var (
	_ string = "Ricall"

	_ = "Render"

	one, two, three, four = 1, 2, 3, 4

	one2, two2, three2, four2 int64 = 1, 2, 3, 4

	twethen string = "Wikkileaks"

	vikvi = make(chan struct{}, 0)

	nik = func() {}

	riz = map[string]string{}

	riz2 map[string]string = map[string]string{
		"day":    "tuesday",
		"second": "10",
	}

	friz = []string{"one", "two", "four"}

	kul = []struct {
		Name string
		Age  int
	}{
		{
			"Rix",
			20,
		},
		{
			"Thery",
			20,
		},
	}

	// Name applies here
	Name  string
	Creed int = 10
	Fall      = 1000

	wilto = []map[string]tokens.Pos{}
	Willt = map[string]interface{}{}
)

type ListOfWiks []api.Rx

type Lists []interface{}

type Rizz map[string]int

type Wilo chan struct{}

type Wiko chan chan []string

type Wiko2 chan func() []string

type Fna func(string) (int, error)

func Bang(m string, p api.Rx) {
	fmt.Printf("Map : %s : %d", m, p)
}

func Hello(n string, pos tokens.Pos, v api.Wilk) {
	bu := map[string]string{"sasd": n}
	fmt.Printf("Map : %s : %d", bu, pos)
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

func (f Function) hello(n string, v, u string, m int) {
	bu := []string{"sasd", n}
	fmt.Printf("List : %s", bu)
}

// Functioner interface
type Functioner interface {
	IsAsync() (bool, error)
	IsVariadic() bool
	Node() *ast.FuncDecl
	Rewrite(string, int)
	Params(v string) []string
	Results() []FunctionResult
}

type FullFn interface {
	Functioner

	Crib() int
}

type FunctionReco struct {
	FunctionResult
	Functioner
	Field string
	Day   int
}

type FunctionResult struct {
	Field string
	Day   int
}

func (f FunctionResult) hello() {
	var name string
	fmt.Printf("Name : %s", name)
}
