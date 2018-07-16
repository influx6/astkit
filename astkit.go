package astkit

import (
	"gitlab.com/gokit/astkit/compiler"
)

// Transform takes provided package path returning a package
// definition with all dependencies.
func Transform(pkg string) (*compiler.Package, error) {
	return TransformWith(pkg, compiler.Cg{})
}

// TransformWith takes provided package path returning a package
// definition with all dependencies using the compiler.Cg provided.
func TransformWith(pkg string, c compiler.Cg) (*compiler.Package, error) {
	program, err := compiler.Load(pkg, c)
	if err != nil {
		return nil, err
	}

	pd, err := compiler.Indexer.Index(pkg, program)
	if err != nil {
		return nil, err
	}
	return pd, nil
}
