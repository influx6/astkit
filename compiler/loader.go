package compiler

import (
	"go/ast"
	"go/build"
	"go/parser"
	"runtime"

	"path/filepath"

	"os"

	internalRuntime "github.com/gokit/astkit/internal/runtime"
	"github.com/gokit/errors"
	"golang.org/x/tools/go/loader"
)

//*********************************************
// Loader
//*********************************************

// Cg contains configuration values used for generating
// package structure.
type Cg struct {
	// GoPath sets the GOPATH value to be used for loading
	// dependencies and packages.
	GoPath string

	// GoRuntime sets the GOROOT path to be used to indicate
	// the location of the Go runtime source.
	GoRuntime string

	// GoArch sets the golang supported architecture which will be used in generating
	// build and structure data.
	GoArch string

	// GOOS sets the golang supported platform which will be used in generating
	// build and structure data.
	GOOS string

	// ExtraArchs is a map expected to be used to indicate processing of more
	// golang supported platforms and a slice of architectures those platforms
	// should be processed for. It is distinct from the Cg.GoAech and Cg.GOOS
	// fields as it indicates you wish to add this platform structures into
	// built data. It comes will added time cost.
	ExtraArchs map[string][]string

	// PackgeDir sets the absolute path to be used for resolving
	// relative path names by the loader.Config. It is used to
	// set the loader.Config.Cwd.
	PackageDir string

	// Errors allows setting the loader.Config.AllowErrors flag
	// to allow processing of package sources even if errors
	// exists.
	Errors bool

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

	// ErrorCheck sets the function to be provided to the type checker.
	ErrorCheck func(error)

	// PlatformError sets the function to be called with provided error
	// received in attempt to process a giving combination of a platform
	// and architecture.
	PlatformError func(err error, arch string, goos string)

	// AfterTypeCheck provides a hook to listen for latest files added after
	// parser has finished type checking for a giving package.
	AfterTypeCheck func(info *loader.PackageInfo, files []*ast.File)
}

func (cg *Cg) init() error {
	if cg.GoArch == "" {
		cg.GoArch = runtime.GOARCH
	}
	if cg.GOOS == "" {
		cg.GOOS = runtime.GOOS
	}
	if cg.PlatformError == nil {
		cg.PlatformError = func(_ error, _ string, _ string) {}
	}
	if cg.ErrorCheck == nil {
		cg.ErrorCheck = func(_ error) {}
	}
	if cg.PackageDir == "" {
		dir, err := os.Getwd()
		if err != nil {
			return err
		}
		cg.PackageDir = dir
	}
	if cg.GoPath == "" {
		cg.GoPath = internalRuntime.GoPath()
	}
	if cg.GoRuntime == "" {
		cg.GoRuntime = internalRuntime.RootPath()
	}
	return nil
}

// Import returns the build.Package for target import path.
func (cg Cg) Import(ctxt *build.Context, importPath, fromDir string, mode build.ImportMode) (*build.Package, error) {
	if cg.Importer != nil {
		if loaded, err := cg.Importer(ctxt, importPath, fromDir, mode); err == nil {
			return loaded, nil
		}
	}
	return ctxt.Import(importPath, fromDir, mode)
}

// Load takes a giving package path which it parses
// returning a structure containing all related filesets
// and parsed AST.
func Load(c *Cg, pkg string, arch string, goos string) (*loader.Program, error) {
	if err := c.init(); err != nil {
		return nil, err
	}

	if arch == "" {
		return nil, errors.New("arch must be supplied")
	}

	if goos == "" {
		return nil, errors.New("goos must be supplied")
	}

	mybuild := build.Default
	mybuild.GOOS = goos
	mybuild.GOARCH = arch
	mybuild.GOPATH = c.GoPath
	mybuild.GOROOT = c.GoRuntime

	var lconfig loader.Config
	lconfig.Build = &mybuild
	lconfig.Cwd = c.PackageDir
	lconfig.AllowErrors = c.Errors
	lconfig.FindPackage = c.Import
	lconfig.TypeChecker.Error = c.ErrorCheck
	lconfig.AfterTypeCheck = c.AfterTypeCheck
	lconfig.ParserMode = parser.ParseComments

	// Add internal packages that should be loaded
	// by default from config.
	for _, elem := range c.Internals {
		if !internalRuntime.PathExist(internalRuntime.FromRuntime(elem)) {
			continue
		}
		lconfig.Import(elem)
	}

	// Add attached packages ensuring they lie within
	// Cg.GoPath if absolute paths.
	for _, elem := range c.Imports {
		if !filepath.IsAbs(elem) && internalRuntime.PathExist(internalRuntime.FromGoPath(elem)) {
			if c.WithTests {
				lconfig.ImportWithTests(elem)
				continue
			}

			lconfig.Import(elem)
			continue
		}

		if rel, err := internalRuntime.WithinToGoPath(elem); err == nil {
			if c.WithTests {
				lconfig.ImportWithTests(rel)
				continue
			}

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
