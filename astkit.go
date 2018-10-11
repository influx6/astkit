package astkit

import (
	"context"

	"github.com/gokit/astkit/compiler"
)

// Transform takes provided package path returning a package
// definition with all dependencies and a map containing all
// indexed packages, else an error if any occurred.
// definition with all dependencies.
func Transform(ctx context.Context, pkg string) (*compiler.Package, map[string]*compiler.Package, error) {
	return TransformWith(ctx, pkg, compiler.Cg{})
}

// TransformWith takes provided package path returning a package
// definition with all dependencies using the compiler.Cg provided.
// It also returns a map containing all indexed packages, else an error
// if any occurred.
func TransformWith(ctx context.Context, pkg string, c compiler.Cg) (*compiler.Package, map[string]*compiler.Package, error) {
	index := compiler.NewIndexer(c)
	return index.Index(ctx, pkg)
}
