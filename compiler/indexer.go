package compiler

import (
	"fmt"
	"go/ast"
	"path/filepath"

	"strconv"

	"sync"

	"gitlab.com/gokit/astkit/internal/compiler"
	"golang.org/x/tools/go/loader"
)

// errors
const (
	ErrNotFound = Error("target not found")
)

var (
	//compiledCache contains cached versions of processed packages.
	compiledCache = struct {
		cl    sync.RWMutex
		cache map[string]*Package
	}{
		cache: map[string]*Package{},
	}
)

// Indexer exposes a package level AstIndexer.
var Indexer AstIndexer

// AstIndexer implements a golang ast index which parses
// a loader.Program returning a Package containing all
// definitions.
type AstIndexer struct{}

// Index takes provided loader.Program returning a Package containing
// parsed structures, types and declarations of all packages.
// It takes the basePkg path as the starting point of indexing, where the
// Package instance return is the basePkg retrieved from the program.
func (indexer AstIndexer) Index(basePkg string, p *loader.Program) (*Package, error) {
	ctxPkg := p.Package(basePkg)
	if ctxPkg == nil {
		return nil, ErrNotFound
	}

	pkg, err := indexer.indexPackage(ctxPkg, p)
	if err != nil {
		return nil, err
	}

	pkg.Name = basePkg
	return pkg, nil
}

// indexPackage takes provided loader.Program returning a Package containing
// parsed structures, types and declarations of all packages.
func (indexer AstIndexer) indexPackage(p *loader.PackageInfo, program *loader.Program) (*Package, error) {
	pkg := &Package{
		Meta:    p,
		Depends: map[string]*Package{},
		Files:   map[string]*PackageFile{},
	}

	for _, file := range p.Files {

		// process package documentation if available.
		if file.Doc != nil && pkg.Doc == nil {
			if err := indexer.doDoc(pkg, file, p, program); err != nil {
				return pkg, err
			}
		}

		pkgFile := new(PackageFile)
		_, begin, end := compiler.GetPosition(program.Fset, file.Pos(), file.End())
		if begin.IsValid() {
			pkgFile.File = filepath.ToSlash(begin.Filename)
		} else if end.IsValid() {
			pkgFile.File = filepath.ToSlash(begin.Filename)
		}

		pkgFile.Name = filepath.Base(pkgFile.File)
		pkgFile.Dir = filepath.ToSlash(filepath.Dir(pkgFile.File))
		pkg.Files[pkgFile.File] = pkgFile

		// process all imports within fileset.
		if err := indexer.doImport(pkg, pkgFile, file, p, program); err != nil {
			return pkg, err
		}

		var errz error
		ast.Inspect(file, func(node ast.Node) bool {
			next, err := indexer.doInspect(node, file, pkg, p, program)
			if err != nil {
				errz = err
				return false
			}
			return next
		})

		if errz != nil {
			return pkg, errz
		}
	}

	return pkg, nil
}

func (indexer AstIndexer) doInspect(node ast.Node, file *ast.File, from *Package, p *loader.PackageInfo, program *loader.Program) (bool, error) {

	fmt.Printf("Node: %#v\n", node)

	return true, nil
}

func (indexer AstIndexer) doImportedPackage(imp Import, from *Package, p *loader.PackageInfo, program *loader.Program) error {

	// if dependency is already within package then skip this.
	if from.HasDependency(imp.Path) {
		return nil
	}

	compiledCache.cl.RLock()
	if pkg, ok := compiledCache.cache[imp.Path]; ok {
		compiledCache.cl.RUnlock()

		from.Depends[imp.Path] = pkg
		return nil
	}
	compiledCache.cl.RUnlock()

	imported := program.Package(imp.Path)
	if imported == nil {
		return Error("failed to find loaded package: " + imp.Path)
	}

	// Have imported package indexed so we can add into
	// dependency map.
	pkg, err := indexer.indexPackage(imported, program)
	if err != nil {
		return err
	}

	from.Depends[imp.Path] = pkg

	compiledCache.cl.Lock()
	compiledCache.cache[imp.Path] = pkg
	compiledCache.cl.Unlock()
	return nil
}

func (indexer AstIndexer) doImport(base *Package, baseFile *PackageFile, f *ast.File, p *loader.PackageInfo, program *loader.Program) error {
	baseFile.Imports = make([]Import, 0, len(f.Imports))

	for _, spec := range f.Imports {
		length, begin, end := compiler.GetPosition(program.Fset, spec.Pos(), spec.End())

		value := spec.Path.Value
		if unquoted, err := strconv.Unquote(value); err == nil {
			value = unquoted
		}

		var imp Import
		imp.Path = value
		imp.Begin = begin.Offset
		imp.End = end.Offset
		imp.Length = length
		imp.Line = begin.Line
		imp.LineEnd = end.Line
		imp.Column = begin.Column
		imp.ColumnEnd = end.Column

		if spec.Name != nil {
			imp.Alias = spec.Name.Name
		}

		// Add import into package file list.
		baseFile.Imports = append(baseFile.Imports, imp)

		// Process import package to ensure all package follow suit and
		// are added internally.
		if err := indexer.doImportedPackage(imp, base, p, program); err != nil {
			return err
		}
	}

	return nil
}

func (indexer AstIndexer) doCommentary(base *Package, baseFile *PackageFile, f *ast.File, p *loader.PackageInfo, program *loader.Program) error {
	baseFile.Commentaries = make([]Doc, 0, len(f.Comments))
	for _, cdoc := range f.Comments {
		if cdoc.Text() == "//" {
			continue
		}

		doc, err := indexer.doCommentGroup(cdoc, f, program)
		if err != nil {
			return err
		}
		baseFile.Commentaries = append(baseFile.Commentaries, doc)
	}
	return nil
}

func (indexer AstIndexer) doDoc(base *Package, f *ast.File, p *loader.PackageInfo, program *loader.Program) error {
	doc, err := indexer.doCommentGroup(f.Doc, f, program)
	if err != nil {
		return err
	}
	base.Doc = &doc
	return nil
}

func (indexer AstIndexer) doCommentGroup(gdoc *ast.CommentGroup, f *ast.File, program *loader.Program) (Doc, error) {
	var doc Doc
	doc.Text = gdoc.Text()
	doc.Parts = make([]DocText, 0, len(gdoc.List))

	// Set the line details of giving commentary.
	length, begin, end := compiler.GetPosition(program.Fset, gdoc.Pos(), gdoc.End())
	doc.Begin = begin.Offset
	doc.End = end.Offset
	doc.Length = length
	doc.Line = begin.Line
	doc.LineEnd = end.Line
	doc.Column = begin.Column
	doc.ColumnEnd = end.Column

	// Add all commentary text for main doc into list.
	for _, comment := range gdoc.List {
		doc.Parts = append(doc.Parts, indexer.doDocText(comment, f, program))
	}

	return doc, nil
}

func (indexer AstIndexer) doDocText(c *ast.Comment, f *ast.File, program *loader.Program) DocText {
	length, begin, end := compiler.GetPosition(program.Fset, c.Pos(), c.End())

	var doc DocText
	doc.Text = c.Text
	doc.Begin = begin.Offset
	doc.End = end.Offset
	doc.Length = length
	doc.Line = begin.Line
	doc.LineEnd = end.Line
	doc.Column = begin.Column
	doc.ColumnEnd = end.Column
	return doc
}

func (indexer AstIndexer) getExprName(n interface{}) (string, error) {
	switch t := n.(type) {
	case *ast.Ident:
		return t.Name, nil
	case *ast.BasicLit:
		return t.Value, nil
	case *ast.SelectorExpr:
		return indexer.getExprName(t.X)
	default:
		return "", Error("unable to get name")
	}
}
