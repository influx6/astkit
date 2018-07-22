package compiler

import (
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

// Indexer implements a golang ast index which parses
// a loader.Program returning a Package containing all
// definitions.
type Indexer struct {
	BasePackage string
	Program     *loader.Program

	waiter  sync.WaitGroup
	arw     sync.RWMutex
	Indexed map[string]*Package
}

// Index takes provided loader.Program returning a Package containing
// parsed structures, types and declarations of all packages.
// It takes the basePkg path as the starting point of indexing, where the
// Package instance return is the basePkg retrieved from the program.
func (indexer *Indexer) Index() (*Package, error) {
	ctxPkg := indexer.Program.Package(indexer.BasePackage)
	if ctxPkg == nil {
		return nil, ErrNotFound
	}

	pkg, err := indexer.indexPackage(indexer.BasePackage, ctxPkg)
	if err != nil {
		return nil, err
	}

	pkg.Name = indexer.BasePackage
	return pkg, nil
}

// indexPackage takes provided loader.Program returning a Package containing
// parsed structures, types and declarations of all packages.
func (indexer *Indexer) indexPackage(targetPackage string, p *loader.PackageInfo) (*Package, error) {
	pkg := &Package{
		Meta:       p,
		Depends:    map[string]*Package{},
		Files:      map[string]*PackageFile{},
		Structs:    map[string]*Struct{},
		Types:      map[string]*AliasType{},
		Interfaces: map[string]*Interface{},
		Functions:  map[string]*Function{},
	}

	indexer.arw.Lock()
	indexer.Indexed[targetPackage] = pkg
	indexer.arw.Unlock()

	errs := make(chan error, 0)
	declrs := make(chan interface{}, 0)

	// Run through all files for package and schedule them to appropriately
	// send
	for _, file := range p.Files {

		pkgFile := new(PackageFile)
		_, begin, end := compiler.GetPosition(indexer.Program.Fset, file.Pos(), file.End())
		if begin.IsValid() {
			pkgFile.File = filepath.ToSlash(begin.Filename)
		} else if end.IsValid() {
			pkgFile.File = filepath.ToSlash(begin.Filename)
		}

		pkgFile.Name = filepath.Base(pkgFile.File)
		pkgFile.Dir = filepath.ToSlash(filepath.Dir(pkgFile.File))
		pkg.Files[pkgFile.File] = pkgFile

		var scope ParseScope
		scope.Info = p
		scope.From = pkg
		scope.File = file
		scope.Indexer = indexer
		scope.Package = pkgFile
		scope.Program = indexer.Program

		// if file has an associated package-level documentation,
		// parse it and add to package documentation.
		if file.Doc != nil {
			doc, err := scope.handleCommentGroup(file.Doc)
			if err != nil {
				errs <- err
				break
			}

			pkg.Docs = append(pkg.Docs, doc)
		}

		indexer.waiter.Add(1)
		go func() {
			defer indexer.waiter.Done()
			scope.Parse(declrs, errs)
		}()
	}

	var perr error

	// Listen to channels about incoming declarations
	// or error and return.
listenLoop:
	for {
		select {
		case declr := <-declrs:
			if err := pkg.Add(declr); err != nil {
				perr = err
				break listenLoop
			}
		case perr = <-errs:
			break listenLoop
		}
	}

	// Ensure all go-routines are done and block
	// here to simulate sync process.
	indexer.waiter.Wait()

	return pkg, perr
}

func (indexer *Indexer) indexImported(path string, res chan interface{}, errs chan error) {
	indexer.arw.RLock()
	if pkg, ok := indexer.Indexed[path]; ok {
		indexer.arw.RUnlock()
		res <- pkg
		return
	}
	indexer.arw.RUnlock()

	imported := indexer.Program.Package(path)
	if imported == nil {
		errs <- Error("failed to find loaded package: " + path)
		return
	}

	// Have imported package indexed so we can add into
	// dependency map.
	pkg, err := indexer.indexPackage(path, imported)
	if err != nil {
		errs <- err
		return
	}

	res <- pkg
}

//******************************************************************************
// Type ast.Visitor Implementations
//******************************************************************************

// ParseScope defines a struct which embodies a current parsing scope
// related to a giving file, package and program.
type ParseScope struct {
	From        *Package
	File        *ast.File
	Indexer     *Indexer
	Package     *PackageFile
	Program     *loader.Program
	Info        *loader.PackageInfo
	GenComments map[*ast.CommentGroup]struct{}
}

// Parse runs through all non-comment based declarations within the
// giving file.
func (b *ParseScope) Parse(res chan interface{}, errs chan error) {

	if err := b.handleImports(res, errs); err != nil {
		errs <- err
		return
	}

	// Parse all structures first, ensuring to add them into package.
	for _, declr := range b.File.Decls {

	}

	// Parse comments not attached to any declared structured.
	if err := b.handleCommentaries(); err != nil {
		errs <- err
	}
}

func (b *ParseScope) handleImports(res chan interface{}, errs chan error) error {
	for _, dependency := range b.File.Imports {
		length, begin, end := compiler.GetPosition(b.Program.Fset, dependency.Pos(), dependency.End())

		value := dependency.Path.Value
		if unquoted, err := strconv.Unquote(value); err == nil {
			value = unquoted
		}

		var imp Import
		imp.Path = value
		imp.File = begin.Filename
		imp.Begin = begin.Offset
		imp.End = end.Offset
		imp.Length = length
		imp.Line = begin.Line
		imp.LineEnd = end.Line
		imp.Column = begin.Column
		imp.ColumnEnd = end.Column

		if dependency.Name != nil {
			imp.Alias = dependency.Name.Name
		}

		// Add import into package file list.
		b.Package.Imports = append(b.Package.Imports, imp)

		go b.Indexer.indexImported(imp.Path, res, errs)

	}
	return nil
}

func (b *ParseScope) handleStructSpec() {

}

func (b *ParseScope) handleFunctionSpec() {

}

func (b *ParseScope) handleChannelSpec() {

}

func (b *ParseScope) handleValueSpec() {

}

func (b *ParseScope) handleMapSpec() {

}

func (b *ParseScope) handleSliceSpec() {

}

func (b *ParseScope) handleCommentaries() error {
	for _, cdoc := range b.File.Comments {
		if _, ok := b.GenComments[cdoc]; ok {
			continue
		}

		if cdoc.Text() == "//" {
			continue
		}

		doc, err := b.handleCommentGroup(cdoc)
		if err != nil {
			return err
		}

		b.Package.Docs = append(b.Package.Docs, doc)
	}
	return nil
}

func (b *ParseScope) handleCommentGroup(gdoc *ast.CommentGroup) (Doc, error) {
	var doc Doc
	doc.Text = gdoc.Text()
	doc.Parts = make([]DocText, 0, len(gdoc.List))

	// Set the line details of giving commentary.
	length, begin, end := compiler.GetPosition(b.Program.Fset, gdoc.Pos(), gdoc.End())
	doc.File = begin.Filename
	doc.Begin = begin.Offset
	doc.End = end.Offset
	doc.Length = length
	doc.Line = begin.Line
	doc.LineEnd = end.Line
	doc.Column = begin.Column
	doc.ColumnEnd = end.Column

	// Add all commentary text for main doc into list.
	for _, comment := range gdoc.List {
		doc.Parts = append(doc.Parts, b.handleDocText(comment))
	}

	return doc, nil
}

func (b *ParseScope) handleDocText(c *ast.Comment) DocText {
	length, begin, end := compiler.GetPosition(b.Program.Fset, c.Pos(), c.End())

	var doc DocText
	doc.File = begin.Filename
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

//******************************************************************************
//  Utilities
//******************************************************************************

func getExprName(n interface{}) (string, error) {
	switch t := n.(type) {
	case *ast.Ident:
		return t.Name, nil
	case *ast.BasicLit:
		return t.Value, nil
	case *ast.SelectorExpr:
		return getExprName(t.X)
	default:
		return "", Error("unable to get name")
	}
}
