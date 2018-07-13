package compiler

import (
	"go/build"

	"go/parser"

	"path/filepath"

	"gitlab.com/gokit/astkit/internal/runtime"
	"golang.org/x/tools/go/loader"
)

// Cg contains configuration values used for generating
// package structure.
type Cg struct {
	// GoPath sets the GOPATH value to be used for loading
	// dependencies and packages.
	GoPath string

	// GoRuntime sets the GOROOT path to be used to indicate
	// the location of the Go runtime source.
	GoRuntime string

	// WithTests indicate if tests for the main
	// package should be loaded.
	WithTests bool

	// Imports holds default packages
	// to be imported for packages to
	// be imported using config.
	//
	// Path must either be package path
	// or absolute paths who lie within
	// provided config.GoPath.
	Imports []string

	// Internals holds default packages
	// to be imported from the runtime internal
	// packages along packages which will be
	// imported using config.
	//
	// Path must valid go paths for internal package
	// and not relative or absolute paths.
	Internals []string

	// Importer provides a custom Import function to be used
	// for file imports if provided. It also will be called first
	// during any import in-case of custom import structure deferring
	// from Golang defined layout. If an error is returned then fallback
	// is done to use build.Context.Import.
	Importer func(ctx *build.Context, importPath string, fromDir string, mode build.ImportMode) (*build.Package, error)
}

func (cg *Cg) init() error {
	if cg.GoPath == "" {
		cg.GoPath = runtime.GoPath()
	}
	if cg.GoRuntime == "" {
		cg.GoRuntime = runtime.RootPath()
	}
	return nil
}

// Import returns the build.Package for target import path.
func (c Cg) Import(ctxt *build.Context, importPath, fromDir string, mode build.ImportMode) (*build.Package, error) {
	if c.Importer != nil {
		if loaded, err := c.Importer(ctxt, importPath, fromDir, mode); err == nil {
			return loaded, nil
		}
	}
	return ctxt.Import(importPath, fromDir, mode)
}

// Load takes a giving package path which it parses
// returning a structure containing all related filesets
// and parsed AST.
func Load(pkg string, c Cg) (*loader.Program, error) {
	if err := c.init(); err != nil {
		return nil, err
	}

	build := build.Default
	build.GOPATH = c.GoPath
	build.GOROOT = c.GoRuntime

	var lconfig loader.Config
	lconfig.Build = &build
	lconfig.FindPackage = c.Import
	lconfig.ParserMode = parser.ParseComments

	// Add internal packages that should be loaded
	// by default from config.
	for _, elem := range c.Internals {
		if !runtime.PathExist(runtime.FromRuntime(elem)) {
			continue
		}
		lconfig.Import(elem)
	}

	// Add attached packages ensuring they lie within
	// Cg.GoPath if absolute paths.
	for _, elem := range c.Imports {
		if !filepath.IsAbs(elem) && runtime.PathExist(runtime.FromGoPath(elem)) {
			lconfig.Import(elem)
			continue
		}

		if rel, err := runtime.WithinToGoPath(elem); err == nil {
			lconfig.Import(rel)
		}
	}

	// Add base package we desire to be loaded.
	lconfig.Import(pkg)

	// Load packages returning any error if seen.
	program, err := lconfig.Load()
	if err != nil {
		return nil, err
	}

	return program, nil
}
