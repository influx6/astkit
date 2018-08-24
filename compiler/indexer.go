package compiler

import (
	"fmt"
	"go/ast"
	"go/types"
	"log"
	"regexp"
	"strings"

	"strconv"

	"sync"

	"golang.org/x/sync/errgroup"

	"path/filepath"

	"context"

	"github.com/gokit/astkit/internal/compiler"
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
	indexed map[string]*Package
}

// Index takes provided loader.Program returning a Package containing
// parsed structures, types and declarations of all packages.
// It takes the basePkg path as the starting point of indexing, where the
// Package instance return is the basePkg retrieved from the program.
// If no error occurred during initial indexing then generated package
// and indexed map of packages is returned during resolution i.e calling
// of the Resolve(map[string]*Package) method.
// if an error occurred during resolution then the indexed package, map
// of other indexed packages and the error that occurred is returned.
// all imports and structures will be processed and returned as a Package
// instance pointer. In case of an error occurring, an incomplete
// package instance pointer and error will be returned.
func (indexer *Indexer) Index(ctx context.Context) (*Package, map[string]*Package, error) {
	if indexer.indexed == nil {
		indexer.indexed = map[string]*Package{}
	}

	ctxPkg := indexer.Program.Package(indexer.BasePackage)
	if ctxPkg == nil {
		return nil, nil, ErrNotFound
	}

	// Run initial package and dependencies indexing.
	pkg, err := indexer.indexPackage(ctx, indexer.BasePackage, ctxPkg)
	if err != nil {
		return nil, nil, err
	}

	// Get all indexed packages map.
	indexed := indexer.indexed

	// Call all indexed packages Resolve() method to ensure
	// all structures are adequately referenced.
	for _, pkg := range indexed {
		if err := pkg.Resolve(indexed); err != nil {
			return pkg, indexed, err
		}
	}

	indexer.waiter.Wait()

	return pkg, indexed, nil
}

// indexPackage takes provided loader.Program returning a Package containing
// parsed structures, types and declarations of all packages.
func (indexer *Indexer) indexPackage(ctx context.Context, targetPackage string, p *loader.PackageInfo) (*Package, error) {
	pkg := &Package{
		Meta:       p,
		Name:       targetPackage,
		Types:      map[string]*Type{},
		Structs:    map[string]*Struct{},
		Depends:    map[string]*Package{},
		Functions:  map[string]*Function{},
		Interfaces: map[string]*Interface{},
		Files:      map[string]*PackageFile{},
	}

	indexer.arw.Lock()
	indexer.indexed[targetPackage] = pkg
	indexer.arw.Unlock()

	// send package has response after processing.
	return pkg, indexer.index(ctx, pkg, p)
}

func (indexer *Indexer) index(ctx context.Context, pkg *Package, p *loader.PackageInfo) error {
	in := make(chan interface{}, 0)
	w, ctx := errgroup.WithContext(ctx)

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
				return err
			}

			pkg.Docs = append(pkg.Docs, doc)
		}

		w.Go(func() error {
			return scope.Parse(ctx, in)
		})
	}

	indexer.waiter.Add(1)
	go func() {
		defer indexer.waiter.Done()
		for elem := range in {
			if err := pkg.Add(elem); err != nil {
				log.Fatalf("Failed to add item into package: %+q", err)
			}
		}
	}()

	err := w.Wait()
	close(in)

	return err
}

func (indexer *Indexer) indexImported(ctx context.Context, path string) (*Package, error) {
	indexer.arw.RLock()
	if pkg, ok := indexer.indexed[path]; ok {
		indexer.arw.RUnlock()
		return pkg, nil
	}
	indexer.arw.RUnlock()

	imported := indexer.Program.Package(path)
	if imported == nil {
		return nil, Error("failed to find loaded package: " + path)
	}

	// Have imported package indexed so we can add into
	// dependency map and send pkg pointer to root.
	return indexer.indexPackage(ctx, path, imported)
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
func (b *ParseScope) Parse(ctx context.Context, in chan interface{}) error {
	if err := b.handleImports(ctx, in); err != nil {
		return err
	}

	// Parse all structures first, ensuring to add them into package.
	for _, declr := range b.File.Decls {
		switch elem := declr.(type) {
		case *ast.FuncDecl:
			if err := b.handleFunctionSpec(elem, in); err != nil {
				return err
			}
		case *ast.GenDecl:
			for _, spec := range elem.Specs {
				if err := b.handleDeclarations(ctx, spec, elem, in); err != nil {
					return err
				}
			}
		case *ast.BadDecl:
			// Do nothing...
		}
	}

	// Parse comments not attached to any declared structured.
	if err := b.handleCommentaries(); err != nil {
		return nil
	}

	return nil
}

func (b *ParseScope) handleDeclarations(ctx context.Context, spec ast.Spec, gen *ast.GenDecl, in chan interface{}) error {
	switch obj := spec.(type) {
	case *ast.ValueSpec:
		if err := b.handleVariable(ctx, obj, spec, gen, in); err != nil {
			return err
		}
	case *ast.TypeSpec:
		switch ty := obj.Type.(type) {
		case *ast.InterfaceType:
			if err := b.handleInterface(ctx, ty, obj, spec, gen, in); err != nil {
				return err
			}
		case *ast.StructType:
			if err := b.handleStruct(ctx, ty, obj, spec, gen, in); err != nil {
				return err
			}
		default:
			if err := b.handleNamedType(ctx, obj, spec, gen, in); err != nil {
				return err
			}
		}
	case *ast.ImportSpec:
		// Do nothing ...
	}
	return nil
}

func (b *ParseScope) handleVariable(ctx context.Context, val *ast.ValueSpec, spec ast.Spec, gen *ast.GenDecl, in chan interface{}) error {
	return nil
}

func (b *ParseScope) handleStruct(ctx context.Context, str *ast.StructType, ty *ast.TypeSpec, spec ast.Spec, gen *ast.GenDecl, res chan interface{}) error {

	return nil
}

func (b *ParseScope) handleInterface(ctx context.Context, str *ast.InterfaceType, ty *ast.TypeSpec, spec ast.Spec, gen *ast.GenDecl, res chan interface{}) error {

	return nil
}

func (b *ParseScope) handleNamedType(ctx context.Context, ty *ast.TypeSpec, spec ast.Spec, gen *ast.GenDecl, res chan interface{}) error {

	return nil
}

func (b *ParseScope) handleImports(ctx context.Context, in chan interface{}) error {
	if b.Package.Imports == nil {
		b.Package.Imports = map[string]Import{}
	}

	for _, dependency := range b.File.Imports {
		source, length, begin, end := compiler.ReadSourceIfPossible(b.Program.Fset, dependency.Pos(), dependency.End())

		value := dependency.Path.Value
		if unquoted, err := strconv.Unquote(value); err == nil {
			value = unquoted
		}

		var imp Import

		if dependency.Doc != nil {
			b.GenComments[dependency.Doc] = struct{}{}

			doc, err := b.handleCommentGroup(dependency.Doc)
			if err != nil {
				return err
			}
			imp.Docs = append(imp.Docs, doc)
		}

		if dependency.Comment != nil {
			b.GenComments[dependency.Comment] = struct{}{}
			doc, err := b.handleCommentGroup(dependency.Comment)
			if err != nil {
				return err
			}
			imp.Docs = append(imp.Docs, doc)
		}

		imp.Path = value
		imp.File = begin.Filename
		imp.Begin = begin.Offset
		imp.Source = string(source)
		imp.End = end.Offset
		imp.Length = length
		imp.Line = begin.Line
		imp.LineEnd = end.Line
		imp.Column = begin.Column
		imp.ColumnEnd = end.Column

		// If it has an alias then set alias to name value.
		if dependency.Name != nil {
			imp.Alias = dependency.Name.Name
		} else {
			baseName := filepath.Base(imp.Path)
			if strings.Contains(baseName, ".") {
				baseName = strings.Split(baseName, ".")[0]
			}
			imp.Alias = baseName
		}

		// Add import into package file list.
		b.Package.Imports[imp.Alias] = imp

		// Index imported separately, has resolution of
		// references will happen later after indexing.
		pkg, err := b.Indexer.indexImported(ctx, imp.Path)
		if err != nil {
			return err
		}

		in <- pkg
	}
	return nil
}

func (b *ParseScope) handleFunctionSpec(fn *ast.FuncDecl, res chan interface{}) error {
	var declr Function
	declr.Name = fn.Name.Name

	if fn.Doc != nil {
		b.GenComments[fn.Doc] = struct{}{}
		doc, err := b.handleCommentGroup(fn.Doc)
		if err != nil {
			return err
		}

		declr.Meta.Doc = doc
	}

	// Set the line details of giving commentary.
	srcd, length, begin, end := compiler.ReadSourceIfPossible(b.Program.Fset, fn.Pos(), fn.End())
	declr.File = begin.Filename
	declr.Source = string(srcd)
	declr.Begin = begin.Offset
	declr.End = end.Offset
	declr.Length = length
	declr.Line = begin.Line
	declr.LineEnd = end.Line
	declr.Column = begin.Column
	declr.ColumnEnd = end.Column

	obj := b.Info.ObjectOf(fn.Name)
	declr.Exported = obj.Exported()
	declr.Meta.Name = obj.Pkg().Name()
	declr.Meta.Path = obj.Pkg().Path()
	declr.Path = strings.Join([]string{obj.Pkg().Path(), fn.Name.Name}, ".")

	var err error
	if fn.Type.Params != nil {
		declr.Arguments, err = b.handleParameterList(fn.Type.Params)
		if err != nil {
			return err
		}
	}

	if fn.Type.Results != nil {
		declr.Returns, err = b.handleParameterList(fn.Type.Results)
		if err != nil {
			return err
		}
	}

	if fn.Recv == nil {
		res <- &declr
		return nil
	}

	res <- &declr
	return nil
}

func (b *ParseScope) handleFieldWithName(f *ast.Field, nm *ast.Ident, t ast.Expr) (Field, error) {
	var p Field
	p.Name = nm.Name

	if f.Doc != nil {
		b.GenComments[f.Doc] = struct{}{}
		if doc, err := b.handleCommentGroup(f.Doc); err != nil {
			p.Docs = append(p.Docs, doc)
		}
	}

	if f.Comment != nil {
		b.GenComments[f.Comment] = struct{}{}
		if doc, err := b.handleCommentGroup(f.Comment); err != nil {
			p.Docs = append(p.Docs, doc)
		}
	}

	obj := b.Info.ObjectOf(fn.Name)
	p.Exported = obj.Exported()
	p.Meta.Name = obj.Pkg().Name()
	p.Meta.Path = obj.Pkg().Path()
	p.Path = strings.Join([]string{obj.Pkg().Path(), fn.Name.Name}, ".")

	// Set the line details of giving commentary.
	srcd, length, begin, end := compiler.ReadSourceIfPossible(b.Program.Fset, f.Pos(), f.End())
	p.File = begin.Filename
	p.Source = string(srcd)
	p.Begin = begin.Offset
	p.End = end.Offset
	p.Length = length
	p.Line = begin.Line
	p.LineEnd = end.Line
	p.Column = begin.Column
	p.ColumnEnd = end.Column

	pAddr := &p

	p.resolver = func(packages map[string]*Package) error {
		tl, tlname, meta, err := GetExprType(b, f, t, packages)
		if err != nil {
			return err
		}

		pAddr.Type = tl
		pAddr.Meta = meta
		pAddr.TypeName = tlname
		return nil
	}

	return p, nil
}

func (b *ParseScope) handleFieldList(set *ast.FieldList) ([]Field, error) {
	var params []Field
	for _, param := range set.List {
		if len(param.Names) == 0 {
			p, err := b.handleField(param, param.Type)
			if err != nil {
				return params, err
			}

			params = append(params, p)
			continue
		}

		// If we have name as a named Field or named return then
		// appropriately
		for _, name := range param.Names {
			p, err := b.handleFieldWithName(param, name, param.Type)
			if err != nil {
				return params, err
			}

			params = append(params, p)
		}
	}
	return params, nil
}

func (b *ParseScope) handleParameterList(set *ast.FieldList) ([]Parameter, error) {
	var params []Parameter
	for _, param := range set.List {
		if len(param.Names) == 0 {
			p, err := b.handleParameter(param, param.Type)
			if err != nil {
				return params, err
			}

			params = append(params, p)
			continue
		}

		// If we have name as a named parameter or named return then
		// appropriately
		for _, name := range param.Names {
			p, err := b.handleParameterWithName(param, name, param.Type)
			if err != nil {
				return params, err
			}

			params = append(params, p)
		}
	}
	return params, nil
}

func (b *ParseScope) handleField(f *ast.Field, t ast.Expr) (Field, error) {
	var p Field

	if f.Doc != nil {
		b.GenComments[f.Doc] = struct{}{}
		if doc, err := b.handleCommentGroup(f.Doc); err != nil {
			p.Docs = append(p.Docs, doc)
		}
	}

	if f.Comment != nil {
		b.GenComments[f.Comment] = struct{}{}
		if doc, err := b.handleCommentGroup(f.Comment); err != nil {
			p.Docs = append(p.Docs, doc)
		}
	}

	// Set the line details of giving commentary.
	srcd, length, begin, end := compiler.ReadSourceIfPossible(b.Program.Fset, f.Pos(), f.End())
	p.File = begin.Filename
	p.Source = string(srcd)
	p.Begin = begin.Offset
	p.End = end.Offset
	p.Length = length
	p.Line = begin.Line
	p.LineEnd = end.Line
	p.Column = begin.Column
	p.ColumnEnd = end.Column

	pAddr := &p

	p.resolver = func(packages map[string]*Package) error {
		tl, tlname, meta, err := GetExprType(b, f, t, packages)
		if err != nil {
			return err
		}

		pAddr.Type = tl
		pAddr.Meta = meta
		pAddr.TypeName = tlname
		return nil
	}

	return p, nil
}

func (b *ParseScope) handleParameterWithName(f *ast.Field, nm *ast.Ident, t ast.Expr) (Parameter, error) {
	var p Parameter
	p.Name = nm.Name

	if f.Doc != nil {
		b.GenComments[f.Doc] = struct{}{}
		if doc, err := b.handleCommentGroup(f.Doc); err != nil {
			p.Docs = append(p.Docs, doc)
		}
	}

	if f.Comment != nil {
		b.GenComments[f.Comment] = struct{}{}
		if doc, err := b.handleCommentGroup(f.Comment); err != nil {
			p.Docs = append(p.Docs, doc)
		}
	}

	// Set the line details of giving commentary.
	srcd, length, begin, end := compiler.ReadSourceIfPossible(b.Program.Fset, f.Pos(), f.End())
	p.File = begin.Filename
	p.Source = string(srcd)
	p.Begin = begin.Offset
	p.End = end.Offset
	p.Length = length
	p.Line = begin.Line
	p.LineEnd = end.Line
	p.Column = begin.Column
	p.ColumnEnd = end.Column

	pAddr := &p
	if elip, ok := t.(*ast.Ellipsis); ok {
		p.IsVariadic = true
		p.resolver = func(packages map[string]*Package) error {
			tl, tlname, meta, err := GetExprType(b, f, elip.Elt, packages)
			if err != nil {
				return err
			}

			pAddr.Type = tl
			pAddr.Meta = meta
			pAddr.TypeName = tlname
			return nil
		}
		return p, nil
	}

	p.resolver = func(packages map[string]*Package) error {
		tl, tlname, meta, err := GetExprType(b, f, t, packages)
		if err != nil {
			return err
		}

		pAddr.Type = tl
		pAddr.Meta = meta
		pAddr.TypeName = tlname
		return nil
	}

	return p, nil
}

func (b *ParseScope) handleParameter(f *ast.Field, t ast.Expr) (Parameter, error) {
	var p Parameter

	if f.Doc != nil {
		b.GenComments[f.Doc] = struct{}{}
		if doc, err := b.handleCommentGroup(f.Doc); err != nil {
			p.Docs = append(p.Docs, doc)
		}
	}

	if f.Comment != nil {
		b.GenComments[f.Comment] = struct{}{}
		if doc, err := b.handleCommentGroup(f.Comment); err != nil {
			p.Docs = append(p.Docs, doc)
		}
	}

	// Set the line details of giving commentary.
	srcd, length, begin, end := compiler.ReadSourceIfPossible(b.Program.Fset, f.Pos(), f.End())
	p.File = begin.Filename
	p.Source = string(srcd)
	p.Begin = begin.Offset
	p.End = end.Offset
	p.Length = length
	p.Line = begin.Line
	p.LineEnd = end.Line
	p.Column = begin.Column
	p.ColumnEnd = end.Column

	pAddr := &p
	if elip, ok := t.(*ast.Ellipsis); ok {
		p.IsVariadic = true
		p.resolver = func(packages map[string]*Package) error {
			tl, tlname, meta, err := GetExprType(b, f, elip.Elt, packages)
			if err != nil {
				return err
			}

			pAddr.Type = tl
			pAddr.Meta = meta
			pAddr.TypeName = tlname
			return nil
		}
		return p, nil
	}

	p.resolver = func(packages map[string]*Package) error {
		tl, tlname, meta, err := GetExprType(b, f, t, packages)
		if err != nil {
			return err
		}

		pAddr.Type = tl
		pAddr.Meta = meta
		pAddr.TypeName = tlname
		return nil
	}

	return p, nil
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
	srcd, length, begin, end := compiler.ReadSourceIfPossible(b.Program.Fset, gdoc.Pos(), gdoc.End())
	doc.Source = string(srcd)
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
	srcd, length, begin, end := compiler.ReadSourceIfPossible(b.Program.Fset, c.Pos(), c.End())

	var doc DocText
	doc.Source = string(srcd)
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

// GetExprType returns the associated type of a giving ast.Expr by tracking the package and type that it
// is declared as.
func GetExprType(base *ParseScope, f *ast.Field, expr ast.Expr, others map[string]*Package) (interface{}, string, Meta, error) {
	var meta Meta

	switch t := expr.(type) {
	case *ast.Ident:
		if obj := base.Info.ObjectOf(t); obj != nil {

		}

		return &Base{
			Name:     t.Name,
			Exported: t.IsExported(),
		}, t.Name, meta, nil

	case *ast.BasicLit:
		return &Base{
			Name:     t.Value,
			Exported: true,
		}, t.Value, meta, nil
	case *ast.StarExpr:
	case *ast.UnaryExpr:
	case *ast.SelectorExpr:
	case *ast.IndexExpr:
	case *ast.CallExpr:
	case *ast.CompositeLit:
	case *ast.ArrayType:
	case *ast.FuncLit:
	case *ast.ParenExpr:
	case *ast.KeyValueExpr:
	case *ast.MapType:
	case *ast.BinaryExpr:
	case *ast.InterfaceType:
	}

	return nil, "", meta, nil
}

// GetTyepFromSet attempts to return a concrete Expr object which implements interface from types.Object provided.
func GetTypeFromSet(base *ParseScope, others map[string]*Package, obj types.Object) (Expr, error) {
	//targetPkg, ok := others[obj.Pkg().Path()]
	//if !ok {
	//	return nil, errors.New("not found")
	//}

	//switch obj.Type().(type) {
	//
	//}

	return nil, nil
}

var (
	moreSpaces = regexp.MustCompile(`\s+`)
	itag       = regexp.MustCompile(`(\w+):"(\w+|[\w,?\s+\w]+)"`)
	annotation = regexp.MustCompile("@(\\w+(:\\w+)?)(\\([.\\s\\S]+\\))?")
)

// GetTags returns all associated tag within provided string.
func GetTags(content string) []Tag {
	var tags []Tag
	cleaned := moreSpaces.ReplaceAllString(content, " ")
	for _, tag := range strings.Split(cleaned, " ") {
		if !itag.MatchString(tag) {
			continue
		}

		res := itag.FindStringSubmatch(tag)
		resValue := strings.Split(res[2], ",")

		tags = append(tags, Tag{
			Text:  res[0],
			Name:  res[1],
			Value: resValue[0],
			Meta:  resValue[1:],
		})
	}
	return tags
}

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

// GetExprCaller gets the expression caller
// e.g. x.Cool() => x
func GetExprCaller(n ast.Node) (ast.Expr, error) {
	switch t := n.(type) {
	case *ast.SelectorExpr:
		return t.X, nil
	case *ast.Ident:
		return nil, nil
	default:
		return nil, fmt.Errorf("util/GetExprCaller: unhandled %T", t)
	}
}

// GetIdentifier gets rightmost identifier if there is one
func GetIdentifier(n ast.Node) (*ast.Ident, error) {
	switch t := n.(type) {
	case *ast.Ident:
		return t, nil
	case *ast.StarExpr:
		return GetIdentifier(t.X)
	case *ast.UnaryExpr:
		return GetIdentifier(t.X)
	case *ast.SelectorExpr:
		return GetIdentifier(t.Sel)
	case *ast.IndexExpr:
		return GetIdentifier(t.X)
	case *ast.CallExpr:
		return GetIdentifier(t.Fun)
	case *ast.CompositeLit:
		return GetIdentifier(t.Type)
	case *ast.FuncDecl:
		return t.Name, nil
	case *ast.ParenExpr:
		return GetIdentifier(t.X)
	case *ast.ArrayType, *ast.MapType, *ast.StructType,
		*ast.ChanType, *ast.FuncType, *ast.InterfaceType,
		*ast.FuncLit, *ast.BinaryExpr:
		return nil, nil
	case *ast.SliceExpr:
		return GetIdentifier(t.X)
	default:
		return nil, fmt.Errorf("GetIdentifier: unhandled %T", n)
	}
}

// ExprToString fn
func ExprToString(n ast.Node) (string, error) {
	switch t := n.(type) {
	case *ast.BasicLit:
		return t.Value, nil
	case *ast.Ident:
		return t.Name, nil
	case *ast.StarExpr:
		return ExprToString(t.X)
	case *ast.UnaryExpr:
		return ExprToString(t.X)
	case *ast.SelectorExpr:
		s, e := ExprToString(t.X)
		if e != nil {
			return "", e
		}
		return s + "." + t.Sel.Name, nil
	case *ast.IndexExpr:
		s, e := ExprToString(t.X)
		if e != nil {
			return "", e
		}
		i, e := ExprToString(t.Index)
		if e != nil {
			return "", e
		}
		return s + "[" + i + "]", nil
	case *ast.CallExpr:
		c, e := ExprToString(t.Fun)
		if e != nil {
			return "", e
		}
		var args []string
		for _, arg := range t.Args {
			a, e := ExprToString(arg)
			if e != nil {
				return "", e
			}
			args = append(args, a)
		}
		return c + "(" + strings.Join(args, ", ") + ")", nil
	case *ast.CompositeLit:
		c, e := ExprToString(t.Type)
		if e != nil {
			return "", e
		}
		var args []string
		for _, arg := range t.Elts {
			a, e := ExprToString(arg)
			if e != nil {
				return "", e
			}
			args = append(args, a)
		}
		return c + "{" + strings.Join(args, ", ") + "}", nil
	case *ast.ArrayType:
		c, e := ExprToString(t.Elt)
		if e != nil {
			return "", e
		}
		return `[]` + c, nil
	case *ast.FuncLit:
		return "func(){}()", nil
	case *ast.ParenExpr:
		x, e := ExprToString(t.X)
		if e != nil {
			return "", e
		}
		return "(" + x + ")", nil
	case *ast.KeyValueExpr:
		k, e := ExprToString(t.Key)
		if e != nil {
			return "", e
		}
		v, e := ExprToString(t.Value)
		if e != nil {
			return "", e
		}
		return "{" + k + ":" + v + "}", nil
	case *ast.MapType:
		k, e := ExprToString(t.Key)
		if e != nil {
			return "", e
		}
		v, e := ExprToString(t.Value)
		if e != nil {
			return "", e
		}
		return "{" + k + ":" + v + "}", nil
	case *ast.BinaryExpr:
		x, e := ExprToString(t.X)
		if e != nil {
			return "", e
		}
		y, e := ExprToString(t.Y)
		if e != nil {
			return "", e
		}
		return x + " " + t.Op.String() + " " + y, nil
	case *ast.InterfaceType:
		return "interface{}", nil
	default:
		return "", fmt.Errorf("util/ExprToString: unhandled %T", n)
	}
}
