package compiler

import (
	"fmt"
	"go/ast"
	"go/token"
	"go/types"
	"log"
	"regexp"
	"strconv"
	"strings"

	"sync"

	"golang.org/x/sync/errgroup"

	"path/filepath"

	"context"

	"github.com/gokit/astkit/internal/compiler"
	"github.com/gokit/errors"
	"golang.org/x/tools/go/loader"
)

// errors
const (
	ErrNotFound = Error("target not found")
)

const (
	blankIdentifier = "_"
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
		Variables:  map[string]*Variable{},
		Functions:  map[string]*Function{},
		Methods:    map[string]*Function{},
		Interfaces: map[string]*Interface{},
		Files:      map[string]*PackageFile{},
	}

	indexer.arw.Lock()
	indexer.indexed[targetPackage] = pkg
	indexer.arw.Unlock()

	// send package has response after processing.
	return pkg, indexer.index(ctx, pkg, p)
}

// index runs logic on a per package basis handling all commentaries, type declarations which
// which be processed and added into provided package reference.
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

// indexImported runs indexing as a fresh package on giving import path, if path is found,
// then a new indexing is spurned and returns a new Package instance at the end else
// returning an error. This exists to allow abstracting the indexing process using
// the import path as target, because of the initial logic for Indexer, as we need
// to be able to skip already indexed packages or register index packages, more so
// if a package already has being indexed, it is just returned.
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
	From     *Package
	File     *ast.File
	Indexer  *Indexer
	Package  *PackageFile
	Program  *loader.Program
	Info     *loader.PackageInfo
	comments map[*ast.CommentGroup]struct{}
}

// Parse runs through all non-comment based declarations within the
// giving file.
func (b *ParseScope) Parse(ctx context.Context, in chan interface{}) error {
	if b.comments == nil {
		b.comments = map[*ast.CommentGroup]struct{}{}
	}

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
			var bad BadExpr
			bad.Location = b.getLocation(elem.Pos(), elem.End())
			in <- bad
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
		if err := b.handleVariables(ctx, obj, spec, gen, in); err != nil {
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

func (b *ParseScope) handleImports(ctx context.Context, in chan interface{}) error {
	if b.Package.Imports == nil {
		b.Package.Imports = map[string]Import{}
	}

	for _, dependency := range b.File.Imports {
		value := dependency.Path.Value
		if unquoted, err := strconv.Unquote(value); err == nil {
			value = unquoted
		}

		var imp Import
		imp.Path = value

		// read location information(line, column, source text etc) for giving type.
		imp.Location = b.getLocation(dependency.Pos(), dependency.End())

		if dependency.Doc != nil {
			b.comments[dependency.Doc] = struct{}{}

			doc, err := b.handleCommentGroup(dependency.Doc)
			if err != nil {
				return err
			}
			imp.Docs = append(imp.Docs, doc)
		}

		if dependency.Comment != nil {
			b.comments[dependency.Comment] = struct{}{}
			doc, err := b.handleCommentGroup(dependency.Comment)
			if err != nil {
				return err
			}
			imp.Docs = append(imp.Docs, doc)
		}

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

func (b *ParseScope) handleVariables(ctx context.Context, val *ast.ValueSpec, spec ast.Spec, gen *ast.GenDecl, in chan interface{}) error {
	for index, named := range val.Names {
		if err := b.handleVariable(ctx, index, named, val, spec, gen, in); err != nil {
			return err
		}
	}
	return nil
}

func (b *ParseScope) handleVariable(ctx context.Context, index int, named *ast.Ident, val *ast.ValueSpec, spec ast.Spec, gen *ast.GenDecl, in chan interface{}) error {
	var declr Variable
	declr.Name = named.Name

	if named.Name == blankIdentifier {
		declr.Blank = true
	}

	// read location information(line, column, source text etc) for giving type.
	if len(val.Names) > 1 {
		declr.Location = b.getLocation(named.Pos(), named.End())
	} else {
		declr.Location = b.getLocation(val.Pos(), val.End())
	}

	if val.Doc != nil {
		b.comments[val.Doc] = struct{}{}
		doc, err := b.handleCommentGroup(val.Doc)
		if err != nil {
			return err
		}

		declr.Doc = doc
	}

	obj := b.Info.ObjectOf(named)
	declr.Exported = obj.Exported()
	declr.Meta.Name = obj.Pkg().Name()
	declr.Meta.Path = obj.Pkg().Path()
	declr.Path = strings.Join([]string{obj.Pkg().Path(), named.Name}, ".")

	if _, ok := obj.(*types.Const); ok {
		declr.Constant = true
	}

	declrAddr := &declr

	if len(val.Values) == 0 {
		declr.resolver = func(others map[string]*Package) error {
			vType, err := b.getTypeFromValueExpr(named, nil, val, others)
			if err != nil {
				return err
			}

			declrAddr.Type = vType
			return nil
		}
		in <- &declr
		return nil
	}

	declr.resolver = func(others map[string]*Package) error {
		vType, err := b.getTypeFromValueExpr(named, val.Values[index], val, others)
		if err != nil {
			return err
		}

		declrAddr.Type = vType
		return nil
	}

	in <- &declr
	return nil
}

func (b *ParseScope) handleStruct(ctx context.Context, str *ast.StructType, ty *ast.TypeSpec, spec ast.Spec, gen *ast.GenDecl, in chan interface{}) error {
	var declr Struct

	var structName string
	if ty.Name != nil {
		structName = ty.Name.Name
	} else {
		structName = "struct"
	}

	declr.Name = structName
	declr.Fields = map[string]Field{}
	declr.Embeds = map[string]*Struct{}
	declr.Composes = map[string]*Interface{}
	declr.Methods = map[string]Function{}
	declr.Location = b.getLocation(ty.Pos(), ty.End())

	if gen.Doc != nil {
		b.comments[gen.Doc] = struct{}{}
		doc, err := b.handleCommentGroup(gen.Doc)
		if err != nil {
			return err
		}

		declr.Doc = doc
	}

	if ty.Doc != nil {
		b.comments[ty.Doc] = struct{}{}
		doc, err := b.handleCommentGroup(ty.Doc)
		if err != nil {
			return err
		}

		declr.Docs = append(declr.Docs, doc)
	}

	if ty.Comment != nil {
		b.comments[ty.Comment] = struct{}{}
		doc, err := b.handleCommentGroup(ty.Comment)
		if err != nil {
			return err
		}

		declr.Docs = append(declr.Docs, doc)
	}

	obj := b.Info.ObjectOf(ty.Name)
	declr.Exported = obj.Exported()
	declr.Meta.Name = obj.Pkg().Name()
	declr.Meta.Path = obj.Pkg().Path()
	declr.Path = strings.Join([]string{obj.Pkg().Path(), structName}, ".")

	var resolvers []ResolverFn

	for _, field := range str.Fields.List {
		if len(field.Names) == 0 {
			fl, err := b.handleField(structName, field, field.Type)
			if err != nil {
				return err
			}
			declr.Fields[fl.Name] = fl
			continue
		}

		for _, name := range field.Names {
			fl, err := b.handleFieldWithName(structName, field, name, field.Type)
			if err != nil {
				return err
			}
			declr.Fields[fl.Name] = fl
		}
	}

	declr.resolver = func(others map[string]*Package) error {
		for _, resolver := range resolvers {
			if err := resolver(others); err != nil {
				return err
			}
		}
		return nil
	}

	in <- &declr
	return nil
}

func (b *ParseScope) handleInterface(ctx context.Context, str *ast.InterfaceType, ty *ast.TypeSpec, spec ast.Spec, gen *ast.GenDecl, in chan interface{}) error {
	var declr Interface
	declr.Name = ty.Name.Name

	declr.Methods = map[string]Function{}
	declr.Composes = map[string]*Interface{}
	declr.Location = b.getLocation(ty.Pos(), ty.End())

	if gen.Doc != nil {
		b.comments[gen.Doc] = struct{}{}
		doc, err := b.handleCommentGroup(gen.Doc)
		if err != nil {
			return err
		}

		declr.Doc = doc
	}

	if ty.Doc != nil {
		b.comments[ty.Doc] = struct{}{}
		doc, err := b.handleCommentGroup(ty.Doc)
		if err != nil {
			return err
		}

		declr.Docs = append(declr.Docs, doc)
	}

	if ty.Comment != nil {
		b.comments[ty.Comment] = struct{}{}
		doc, err := b.handleCommentGroup(ty.Comment)
		if err != nil {
			return err
		}

		declr.Docs = append(declr.Docs, doc)
	}

	obj := b.Info.ObjectOf(ty.Name)
	declr.Exported = obj.Exported()
	declr.Meta.Name = obj.Pkg().Name()
	declr.Meta.Path = obj.Pkg().Path()
	declr.Path = strings.Join([]string{obj.Pkg().Path(), ty.Name.Name}, ".")

	var resolvers []ResolverFn

	declrAddr := &declr
	for _, field := range str.Methods.List {
		if len(field.Names) == 0 {
			func(f *ast.Field, tx ast.Expr) {
				resolvers = append(resolvers, func(others map[string]*Package) error {
					return b.handleEmbeddedInterface(ty.Name.Name, f, tx, others, declrAddr)
				})
			}(field, field.Type)
			continue
		}

		for _, name := range field.Names {
			fn, err := b.handleFunctionFieldWithName(ty.Name.Name, field, name, field.Type)
			if err != nil {
				return err
			}
			declr.Methods[fn.Name] = fn
		}
	}

	declr.resolver = func(others map[string]*Package) error {
		for _, resolver := range resolvers {
			if err := resolver(others); err != nil {
				return err
			}
		}
		return nil
	}

	in <- &declr
	return nil
}

func (b *ParseScope) handleNamedType(ctx context.Context, ty *ast.TypeSpec, spec ast.Spec, gen *ast.GenDecl, in chan interface{}) error {
	var declr Type
	declr.Methods = map[string]Function{}
	declr.Location = b.getLocation(ty.Pos(), ty.End())

	if gen.Doc != nil {
		b.comments[gen.Doc] = struct{}{}
		doc, err := b.handleCommentGroup(gen.Doc)
		if err != nil {
			return err
		}

		declr.Doc = doc
	}

	if ty.Doc != nil {
		b.comments[ty.Doc] = struct{}{}
		doc, err := b.handleCommentGroup(ty.Doc)
		if err != nil {
			return err
		}

		declr.Docs = append(declr.Docs, doc)
	}

	if ty.Comment != nil {
		b.comments[ty.Comment] = struct{}{}
		doc, err := b.handleCommentGroup(ty.Comment)
		if err != nil {
			return err
		}

		declr.Docs = append(declr.Docs, doc)
	}

	obj := b.Info.ObjectOf(ty.Name)
	declr.Exported = obj.Exported()
	declr.Meta.Name = obj.Pkg().Name()
	declr.Meta.Path = obj.Pkg().Path()
	declr.Path = strings.Join([]string{obj.Pkg().Path(), ty.Name.Name}, ".")

	declrAddr := &declr
	declr.resolver = func(others map[string]*Package) error {
		// If we are dealing with a selector expression, most
		// probably it's calling external type from external package
		// we need to secure type name.
		if sel, ok := ty.Type.(*ast.SelectorExpr); ok {
			meta, err := b.transformSelectExprToMeta(sel, others)
			if err != nil {
				return err
			}

			declrAddr.Meta = meta
		}

		vType, err := b.getTypeFromTypeSpecExpr(ty.Type, ty, others)
		if err != nil {
			return err
		}

		declrAddr.Points = vType
		return nil
	}

	in <- &declr
	return nil
}

func (b *ParseScope) handleFunctionSpec(fn *ast.FuncDecl, in chan interface{}) error {
	var declr Function
	declr.Name = fn.Name.Name

	if fn.Doc != nil {
		b.comments[fn.Doc] = struct{}{}
		doc, err := b.handleCommentGroup(fn.Doc)
		if err != nil {
			return err
		}

		declr.Doc = doc
	}

	// read location information(line, column, source text etc) for giving type.
	declr.Location = b.getLocation(fn.Pos(), fn.End())

	obj := b.Info.ObjectOf(fn.Name)
	declr.Exported = obj.Exported()
	declr.Meta.Name = obj.Pkg().Name()
	declr.Meta.Path = obj.Pkg().Path()

	var err error
	if fn.Type.Params != nil {
		declr.Arguments, err = b.handleParameterList(fn.Name.Name, fn.Type.Params)
		if err != nil {
			return err
		}
	}

	if fn.Type.Results != nil {
		declr.Returns, err = b.handleParameterList(fn.Name.Name, fn.Type.Results)
		if err != nil {
			return err
		}
	}

	if fn.Recv == nil {
		declr.Path = strings.Join([]string{obj.Pkg().Path(), fn.Name.Name}, ".")
		in <- &declr
		return nil
	}

	if len(fn.Recv.List) == 0 {
		in <- &declr
		return nil
	}

	target := fn.Recv.List[0]
	if len(target.Names) != 0 {
		declr.Path = strings.Join([]string{obj.Pkg().Path(), fn.Recv.List[0].Names[0].Name, fn.Name.Name}, ".")
	} else {
		ident := target.Type.(*ast.Ident)
		declr.Path = strings.Join([]string{obj.Pkg().Path(), ident.Name, fn.Name.Name}, ".")
	}

	fnAddr := &declr
	owner := fn.Recv.List[0]

	declr.resolver = func(others map[string]*Package) error {
		ownerType, err := b.getTypeFromFieldExpr(owner.Type, owner, others)
		if err != nil {
			return err
		}

		switch owner := ownerType.(type) {
		case *Struct:
			owner.Methods[declr.Name] = declr
		case *Interface:
			owner.Methods[declr.Name] = declr
		case *Type:
			owner.Methods[declr.Name] = declr
		}

		fnAddr.IsMethod = true
		fnAddr.Owner = ownerType
		return nil
	}

	in <- &declr
	return nil
}

func (b *ParseScope) handleFunctionLit(fn *ast.FuncLit) (Function, error) {
	declr, err := b.handleFunctionType("func", fn.Type)
	if err != nil {
		return Function{}, err
	}

	// read location information(line, column, source text etc) for giving type.
	declr.Location = b.getLocation(fn.Pos(), fn.End())

	return declr, nil
}

func (b *ParseScope) handleFunctionType(name string, fn *ast.FuncType) (Function, error) {
	var declr Function

	// read location information(line, column, source text etc) for giving type.
	declr.Location = b.getLocation(fn.Pos(), fn.End())

	var err error
	if fn.Params != nil {
		declr.Arguments, err = b.handleParameterList(name, fn.Params)
		if err != nil {
			return declr, err
		}
	}

	if fn.Results != nil {
		declr.Returns, err = b.handleParameterList(name, fn.Results)
		if err != nil {
			return declr, err
		}
	}

	return declr, nil
}

func (b *ParseScope) handleFunctionFieldList(ownerName string, set *ast.FieldList) ([]Function, error) {
	var params []Function
	for _, param := range set.List {
		if len(param.Names) == 0 {
			p, err := b.handleFunctionField(ownerName, param, param.Type)
			if err != nil {
				return params, err
			}

			params = append(params, p)
			continue
		}

		// If we have name as a named Field or named return then
		// appropriately
		for _, name := range param.Names {
			p, err := b.handleFunctionFieldWithName(ownerName, param, name, param.Type)
			if err != nil {
				return params, err
			}

			params = append(params, p)
		}
	}
	return params, nil
}

func (b *ParseScope) handleFieldList(ownerName string, set *ast.FieldList) ([]Field, error) {
	var params []Field
	for _, param := range set.List {
		if len(param.Names) == 0 {
			p, err := b.handleField(ownerName, param, param.Type)
			if err != nil {
				return params, err
			}

			params = append(params, p)
			continue
		}

		// If we have name as a named Field or named return then
		// appropriately
		for _, name := range param.Names {
			p, err := b.handleFieldWithName(ownerName, param, name, param.Type)
			if err != nil {
				return params, err
			}

			params = append(params, p)
		}
	}
	return params, nil
}

func (b *ParseScope) handleFieldMap(ownerName string, set *ast.FieldList) (map[string]Field, error) {
	params := map[string]Field{}

	// If we have name as a named Field or named return then
	// appropriately
	for _, param := range set.List {
		for _, name := range param.Names {
			p, err := b.handleFieldWithName(ownerName, param, name, param.Type)
			if err != nil {
				return params, err
			}

			params[p.Name] = p
		}
	}

	return params, nil
}

func (b *ParseScope) handleParameterList(fnName string, set *ast.FieldList) ([]Parameter, error) {
	var params []Parameter
	for _, param := range set.List {
		if len(param.Names) == 0 {
			p, err := b.handleParameter(fnName, param, param.Type)
			if err != nil {
				return params, err
			}

			params = append(params, p)
			continue
		}

		// If we have name as a named parameter or named return then
		// appropriately
		for _, name := range param.Names {
			p, err := b.handleParameterWithName(fnName, param, name, param.Type)
			if err != nil {
				return params, err
			}

			params = append(params, p)
		}
	}
	return params, nil
}

func (b *ParseScope) handleEmbeddedInterface(ownerName string, f *ast.Field, t ast.Expr, others map[string]*Package, host *Interface) error {
	identity, err := b.getTypeFromFieldExpr(t, f, others)
	if err != nil {
		return err
	}

	embedded, ok := identity.(*Interface)
	if !ok {
		return errors.New("Expected type should be an interface: %#v\n", identity)
	}

	host.Composes[identity.ID()] = embedded
	return nil
}

func (b *ParseScope) handleFunctionField(ownerName string, f *ast.Field, t ast.Expr) (Function, error) {
	var p Function

	id, ok := t.(*ast.Ident)
	if !ok {
		return p, errors.New("Expected *ast.Ident as ast.Expr for handleFunctionField")
	}

	obj := b.Info.ObjectOf(id)
	p.Exported = obj.Exported()
	p.Path = strings.Join([]string{obj.Pkg().Path(), ownerName, id.Name}, ".")

	// read location information(line, column, source text etc) for giving type.
	p.Location = b.getLocation(f.Pos(), f.End())

	if f.Doc != nil {
		b.comments[f.Doc] = struct{}{}
		doc, cerr := b.handleCommentGroup(f.Doc)
		if cerr != nil {
			return p, cerr
		}
		p.Docs = append(p.Docs, doc)
	}

	if f.Comment != nil {
		b.comments[f.Comment] = struct{}{}
		doc, cerr := b.handleCommentGroup(f.Comment)
		if cerr != nil {
			return p, cerr
		}
		p.Docs = append(p.Docs, doc)
	}

	pAddr := &p
	p.resolver = func(others map[string]*Package) error {
		mtype, err := b.getTypeFromFieldExpr(t, f, others)
		if err != nil {
			return err
		}

		pAddr.Owner = mtype
		return nil
	}

	return p, nil
}

func (b *ParseScope) handleFunctionFieldWithName(ownerName string, f *ast.Field, nm *ast.Ident, t ast.Expr) (Function, error) {
	fnType, ok := f.Type.(*ast.FuncType)
	if !ok {
		return Function{}, errors.New("failed to extract function type from Field")
	}

	p, err := b.handleFunctionType(nm.Name, fnType)
	if err != nil {
		return Function{}, err
	}

	p.Name = nm.Name

	if f.Doc != nil {
		b.comments[f.Doc] = struct{}{}
		doc, cerr := b.handleCommentGroup(f.Doc)
		if cerr != nil {
			return p, cerr
		}
		p.Docs = append(p.Docs, doc)
	}

	if f.Comment != nil {
		b.comments[f.Comment] = struct{}{}
		doc, cerr := b.handleCommentGroup(f.Comment)
		if cerr != nil {
			return p, cerr
		}
		p.Docs = append(p.Docs, doc)
	}

	obj := b.Info.ObjectOf(nm)
	p.Exported = obj.Exported()
	p.Path = strings.Join([]string{obj.Pkg().Path(), ownerName, nm.Name}, ".")

	// read location information(line, column, source text etc) for giving type.
	p.Location = b.getLocation(f.Pos(), f.End())

	if fnType.Params != nil {
		p.Arguments, err = b.handleParameterList(nm.Name, fnType.Params)
		if err != nil {
			return p, err
		}
	}

	if fnType.Results != nil {
		p.Returns, err = b.handleParameterList(nm.Name, fnType.Results)
		if err != nil {
			return p, err
		}
	}

	return p, nil
}

func (b *ParseScope) handleField(ownerName string, f *ast.Field, t ast.Expr) (Field, error) {
	var p Field

	if f.Tag != nil {
		p.Tags = GetTags(f.Tag.Value)
	}

	// read location information(line, column, source text etc) for giving type.
	p.Location = b.getLocation(f.Pos(), f.End())

	if f.Doc != nil {
		b.comments[f.Doc] = struct{}{}
		doc, cerr := b.handleCommentGroup(f.Doc)
		if cerr != nil {
			return p, cerr
		}
		p.Docs = append(p.Docs, doc)
	}

	if f.Comment != nil {
		b.comments[f.Comment] = struct{}{}
		doc, cerr := b.handleCommentGroup(f.Comment)
		if cerr != nil {
			return p, cerr
		}
		p.Docs = append(p.Docs, doc)
	}

	pAddr := &p
	p.resolver = func(others map[string]*Package) error {
		// If we are dealing with a selector expression, most
		// probably it's calling external type from external package
		// we need to secure type name.
		if sel, ok := t.(*ast.SelectorExpr); ok {
			meta, err := b.transformSelectExprToMeta(sel, others)
			if err != nil {
				return err
			}

			pAddr.Meta = meta
		}

		tl, err := b.getTypeFromFieldExpr(t, f, others)
		if err != nil {
			return err
		}

		pAddr.Type = tl
		return nil
	}

	return p, nil
}

func (b *ParseScope) handleFieldWithName(ownerName string, f *ast.Field, nm *ast.Ident, t ast.Expr) (Field, error) {
	var p Field
	p.Name = nm.Name

	if f.Tag != nil {
		p.Tags = GetTags(f.Tag.Value)
	}

	if f.Doc != nil {
		b.comments[f.Doc] = struct{}{}
		doc, cerr := b.handleCommentGroup(f.Doc)
		if cerr != nil {
			return p, cerr
		}
		p.Docs = append(p.Docs, doc)
	}

	if f.Comment != nil {
		b.comments[f.Comment] = struct{}{}
		doc, cerr := b.handleCommentGroup(f.Comment)
		if cerr != nil {
			return p, cerr
		}
		p.Docs = append(p.Docs, doc)
	}

	obj := b.Info.ObjectOf(nm)
	p.Exported = obj.Exported()
	p.Path = strings.Join([]string{obj.Pkg().Path(), ownerName, nm.Name}, ".")

	// read location information(line, column, source text etc) for giving type.
	p.Location = b.getLocation(f.Pos(), f.End())

	pAddr := &p

	p.resolver = func(others map[string]*Package) error {
		// If we are dealing with a selector expression, most
		// probably it's calling external type from external package
		// we need to secure type name.
		if sel, ok := t.(*ast.SelectorExpr); ok {
			meta, err := b.transformSelectExprToMeta(sel, others)
			if err != nil {
				return err
			}

			pAddr.Meta = meta
		}

		tl, err := b.getTypeFromFieldExpr(t, f, others)
		if err != nil {
			return err
		}

		pAddr.Type = tl
		return nil
	}

	return p, nil
}

func (b *ParseScope) handleParameter(fnName string, f *ast.Field, t ast.Expr) (Parameter, error) {
	var p Parameter

	if f.Doc != nil {
		b.comments[f.Doc] = struct{}{}
		doc, cerr := b.handleCommentGroup(f.Doc)
		if cerr != nil {
			return p, cerr
		}
		p.Docs = append(p.Docs, doc)
	}

	if f.Comment != nil {
		b.comments[f.Comment] = struct{}{}
		doc, cerr := b.handleCommentGroup(f.Comment)
		if cerr != nil {
			return p, cerr
		}
		p.Docs = append(p.Docs, doc)
	}

	// read location information(line, column, source text etc) for giving type.
	p.Location = b.getLocation(f.Pos(), f.End())

	pAddr := &p
	if elip, ok := t.(*ast.Ellipsis); ok {
		p.IsVariadic = true
		p.resolver = func(others map[string]*Package) error {
			tl, err := b.getTypeFromFieldExpr(elip.Elt, f, others)
			if err != nil {
				return err
			}

			pAddr.Type = tl
			return nil
		}
		return p, nil
	}

	p.resolver = func(others map[string]*Package) error {
		// If we are dealing with a selector expression, most
		// probably it's calling external type from external package
		// we need to secure type name.
		if sel, ok := t.(*ast.SelectorExpr); ok {
			meta, err := b.transformSelectExprToMeta(sel, others)
			if err != nil {
				return err
			}

			pAddr.Meta = meta
		}

		tl, err := b.getTypeFromFieldExpr(t, f, others)
		if err != nil {
			return err
		}

		pAddr.Type = tl
		return nil
	}

	return p, nil
}

func (b *ParseScope) handleParameterWithName(fnName string, f *ast.Field, nm *ast.Ident, t ast.Expr) (Parameter, error) {
	var p Parameter
	p.Name = nm.Name

	// read location information(line, column, source text etc) for giving type.
	p.Location = b.getLocation(f.Pos(), f.End())

	if f.Doc != nil {
		b.comments[f.Doc] = struct{}{}
		doc, cerr := b.handleCommentGroup(f.Doc)
		if cerr != nil {
			return p, cerr
		}
		p.Docs = append(p.Docs, doc)
	}

	if f.Comment != nil {
		b.comments[f.Comment] = struct{}{}
		doc, cerr := b.handleCommentGroup(f.Comment)
		if cerr != nil {
			return p, cerr
		}
		p.Docs = append(p.Docs, doc)
	}

	obj := b.Info.ObjectOf(nm)
	p.Path = strings.Join([]string{obj.Pkg().Path(), fnName, nm.Name}, ".")

	pAddr := &p
	if elip, ok := t.(*ast.Ellipsis); ok {
		p.IsVariadic = true
		p.resolver = func(others map[string]*Package) error {
			tl, err := b.getTypeFromFieldExpr(elip.Elt, f, others)
			if err != nil {
				return err
			}

			pAddr.Type = tl
			return nil
		}
		return p, nil
	}

	p.resolver = func(others map[string]*Package) error {
		// If we are dealing with a selector expression, most
		// probably it's calling external type from external package
		// we need to secure type name.
		if sel, ok := t.(*ast.SelectorExpr); ok {
			meta, err := b.transformSelectExprToMeta(sel, others)
			if err != nil {
				return err
			}

			pAddr.Meta = meta
		}

		tl, err := b.getTypeFromFieldExpr(t, f, others)
		if err != nil {
			return err
		}

		pAddr.Type = tl
		return nil
	}

	return p, nil
}

func (b *ParseScope) handleCommentaries() error {
	for _, cdoc := range b.File.Comments {
		if _, ok := b.comments[cdoc]; ok {
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
	var loc Location
	loc.Source = string(srcd)
	loc.File = begin.Filename
	loc.Begin = begin.Offset
	loc.End = end.Offset
	loc.Length = length
	loc.Line = begin.Line
	loc.LineEnd = end.Line
	loc.Column = begin.Column
	loc.ColumnEnd = end.Column

	doc.Location = &loc

	// Add all commentary text for main doc into list.
	for _, comment := range gdoc.List {
		doc.Parts = append(doc.Parts, b.handleDocText(comment))
	}

	return doc, nil
}

func (b *ParseScope) handleDocText(c *ast.Comment) DocText {
	srcd, length, begin, end := compiler.ReadSourceIfPossible(b.Program.Fset, c.Pos(), c.End())

	var doc DocText
	doc.Text = c.Text

	var loc Location
	loc.Source = string(srcd)
	loc.File = begin.Filename
	loc.Begin = begin.Offset
	loc.End = end.Offset
	loc.Length = length
	loc.Line = begin.Line
	loc.LineEnd = end.Line
	loc.Column = begin.Column
	loc.ColumnEnd = end.Column
	doc.Location = &loc
	return doc
}

func (b *ParseScope) getLocation(beginPos token.Pos, endPos token.Pos) Location {
	srcd, length, begin, end := compiler.ReadSourceIfPossible(b.Program.Fset, beginPos, endPos)

	var loc Location
	loc.Source = string(srcd)
	loc.File = begin.Filename
	loc.Begin = begin.Offset
	loc.End = end.Offset
	loc.Length = length
	loc.Line = begin.Line
	loc.LineEnd = end.Line
	loc.Column = begin.Column
	loc.ColumnEnd = end.Column
	return loc
}

func (b *ParseScope) getImport(aliasName string) (Import, error) {
	if imp, ok := b.Package.Imports[aliasName]; ok {
		return imp, nil
	}
	return Import{}, errors.Wrap(ErrNotFound, "import path with alias %q not found", aliasName)
}

func (b *ParseScope) getTypeFromTypeSpecExpr(e ast.Expr, t *ast.TypeSpec, others map[string]*Package) (Identity, error) {
	fmt.Printf("GetTypeFromTypeSpec: %#v -> %#v\n\n", e, t)

	base, err := b.transformTypeFor(e, others)
	if err != nil {
		return nil, err
	}
	return base, nil
}

func (b *ParseScope) getTypeFromFieldExpr(e ast.Expr, f *ast.Field, others map[string]*Package) (Identity, error) {
	fmt.Printf("GetTypeFromField: %#v -> %#v\n\n", e, f)
	base, err := b.transformTypeFor(e, others)
	if err != nil {
		return nil, err
	}
	return base, nil
}

func (b *ParseScope) getTypeFromValueExpr(f *ast.Ident, val ast.Expr, v *ast.ValueSpec, others map[string]*Package) (Identity, error) {
	var err error
	var base Identity

	if v.Type != nil {
		base, err = b.transformTypeFor(v.Type, others)
	} else {
		base, err = b.transformTypeFor(f, others)
	}

	if err != nil {
		return nil, err
	}

	if docs, ok := base.(commentaryDocs); ok && val != nil {
		if v.Doc != nil {
			doc, cerr := b.handleCommentGroup(v.Doc)
			if cerr != nil {
				return base, cerr
			}
			docs.SetDoc(doc)
		}
		if v.Comment != nil {
			doc, cerr := b.handleCommentGroup(v.Comment)
			if cerr != nil {
				return base, cerr
			}
			docs.AddDoc(doc)
		}
	}

	if val == nil {
		return base, nil
	}

	if geoClone, ok := base.(cloneLocation); ok {
		lm := b.getLocation(val.Pos(), val.End())
		geoClone.Clone(lm)
	}

	//fmt.Printf("Val[%q]: %#v \n", f.Name, val)
	//fmt.Printf("GetTo[%q]: %#v \n\n", f.Name, base)
	return b.processValues(base, val, v, others)
}

func (b *ParseScope) processValues(owner Identity, value interface{}, cave *ast.ValueSpec, others map[string]*Package) (Identity, error) {
	//fmt.Printf("Val:TT %#v\n", value)

	switch rbase := owner.(type) {
	case *Base:
		switch vt := value.(type) {
		case *ast.Ident:
			rbase.Value = vt.Name
		case *ast.BasicLit:
			rbase.Value = vt.Value
		default:
			//return nil, errors.New("invalid type for value expected *ast.Ident")
		}

		return rbase, nil
	case *List:
	case *Map:
	}
	return owner, nil
}

func (b *ParseScope) transformTypeFor(e interface{}, others map[string]*Package) (Identity, error) {
	switch core := e.(type) {
	case types.Object:
		return b.transformObject(core, others)
	case *ast.Ident:
		return b.transformIdent(core, others)
	case *types.Struct:
		return b.transformStruct(core, others)
	case *types.Basic:
		return b.transformBasic(core, others)
	case *ast.BasicLit:
		return b.transformBasicLit(core, others)
	case *ast.CallExpr:
		return b.transformCallExpr(core, others)
	case *ast.CompositeLit:
		return b.transformCompositeLit(core, others)
	case *ast.StarExpr:
		return b.transformStarExpr(core, others)
	case *ast.UnaryExpr:
		return b.transformUnaryExpr(core, others)
	case *ast.SelectorExpr:
		return b.transformSelectorExpr(core, others)
	case *ast.IndexExpr:
		return b.transformIndexExpr(core, others)
	case *types.Named:
		return b.transformNamed(core, others)
	case *types.Slice:
		return b.transformSlice(core, others)
	case *types.Array:
		return b.transformArray(core, others)
	case *types.Pointer:
		return b.transformPointer(core, others)
	case *ast.ArrayType:
		return b.transformArrayType(core, others)
	case *ast.FuncLit:
		return b.transformFuncLit(core, others)
	case *ast.ParenExpr:
		return b.transformParentExpr(core, others)
	case *ast.MapType:
		return b.transformMapType(core, others)
	case *types.Map:
		return b.transformMap(core, others)
	case *ast.SliceExpr:
		return b.transformSliceExpr(core, others)
	case *ast.ChanType:
		return b.transformChanType(core, others)
	case *types.Chan:
		return b.transformChan(core, others)
	case *ast.KeyValueExpr:
		return b.transformKeyValueExpr(core, others)
	case *types.Signature:
		return b.transformSignature(core, others)
	case *ast.FuncType:
		return b.transformFuncType(core, others)
	case *ast.BinaryExpr:
		return b.transformBinaryExpr(core, others)
	}

	return nil, errors.Wrap(ErrNotFound, "unable to find type: %#v", e)
}

func (b *ParseScope) transformIdent(e *ast.Ident, others map[string]*Package) (Identity, error) {
	if obj := b.Info.ObjectOf(e); obj != nil {
		return b.transformTypeFor(obj, others)
	}
	return nil, errors.New("expected to have types.Object for %#v", e)
}

func (b *ParseScope) transformBasic(e *types.Basic, others map[string]*Package) (Identity, error) {
	return BaseFor(e.Name()), nil
}

func (b *ParseScope) transformObject(e types.Object, others map[string]*Package) (Identity, error) {
	// Is this a package-less type, if so then we need to handle type.
	if e.Pkg() == nil {
		return b.transformPackagelessObject(e.Type(), e, others)
	}

	switch tm := e.Type().(type) {
	case *types.Basic:
		return b.transformTypeFor(tm, others)
	case *types.Signature:
		return b.transformSignatureWithObject(tm, e, others)
	case *types.Named:
		return b.transformNamedWithObject(tm, e, others)
	default:
		return b.transformTypeFor(tm, others)
	}
}

func (b *ParseScope) transformPackagelessObject(te types.Type, obj types.Object, others map[string]*Package) (Identity, error) {
	switch tm := te.(type) {
	case *types.Basic:
		return b.transformTypeFor(tm, others)
	case *types.Named:
		return b.transformPackagelessNamedWithObject(tm, obj, others)
	}
	return nil, errors.New("package-less type: %#v", te)
}

func (b *ParseScope) transformPackagelessNamedWithObject(e *types.Named, obj types.Object, others map[string]*Package) (Identity, error) {
	switch tm := e.Underlying().(type) {
	case *types.Struct:
		return b.transformStructWithNamed(tm, e, others)
	case *types.Interface:
		return b.transformInterface(tm, e, others)
	}
	return nil, errors.New("package-less named type: %#v", e)
}

func (b *ParseScope) transformNamedWithObject(e *types.Named, obj types.Object, others map[string]*Package) (Identity, error) {
	fmt.Printf("\nNamedWithObj: %#v -> %#v --> %#v\n", e.Obj(), e.Underlying(), obj.Type())
	switch tm := e.Underlying().(type) {
	case *types.Struct:
		return b.transformStructWithObject(tm, e, obj, others)
	case *types.Interface:
		return b.transformInterfaceWithObject(tm, e, obj, others)
	case *types.Slice:
		return b.transformTypeFor(tm, others)
	case *types.Map:
		return b.transformTypeFor(tm, others)
	case *types.Array:
		return b.transformTypeFor(tm, others)
	case *types.Chan:
		return b.transformTypeFor(tm, others)
	case *types.Pointer:
	case *types.Basic:
		return b.transformTypeFor(tm, others)
	default:
		return b.transformNamedTypeWithObject(e.Obj(), e, obj, others)
	}
	return &Type{Methods: map[string]Function{}}, nil
}

func (b *ParseScope) transformNamedTypeWithObject(typedNamed *types.TypeName, named *types.Named, obj types.Object, others map[string]*Package) (Identity, error) {
	fmt.Printf("NamedTypeWithObj[%q]: %#v -> %#v\n", named.Obj().Name(), typedNamed, named.Underlying())
	return &Type{Methods: map[string]Function{}}, nil
}

func (b *ParseScope) transformSignatureWithObject(e *types.Signature, obj types.Object, others map[string]*Package) (Identity, error) {
	fmt.Printf("Signature: %#v -> %#v\n", e, e.Underlying())
	return &Type{Methods: map[string]Function{}}, nil
}

func (b *ParseScope) transformStructWithObject(e *types.Struct, named *types.Named, obj types.Object, others map[string]*Package) (Identity, error) {
	fmt.Printf("StructWithObj[%q]: %#v -> %#v -> %#v\n", named.Obj().Name(), e, e.Underlying(), obj.Pkg())
	myPackage := obj.Pkg()

	targetPackage, ok := others[myPackage.Path()]
	if !ok {
		return nil, errors.Wrap(ErrNotFound, "Unable to find target package %q", obj.Pkg().Path())
	}

	ref := strings.Join([]string{targetPackage.Name, named.Obj().Name()}, ".")
	if target, ok := targetPackage.Structs[ref]; ok {
		return target, nil
	}

	imported := myPackage.Imports()
	for _, stage := range imported {
		fmt.Printf("Stage[%q] -> %#v\n", named.Obj().Name(), stage)

		targetPackage, ok := others[stage.Path()]
		if !ok {
			return nil, errors.Wrap(ErrNotFound, "Unable to find imported package %q", stage.Path())
		}

		ref := strings.Join([]string{targetPackage.Name, named.Obj().Name()}, ".")
		if target, ok := targetPackage.Structs[ref]; ok {
			return target, nil
		}
	}

	return nil, errors.Wrap(ErrNotFound, "Unable to find target struct %q from package or its imported set", named.Obj().Name())
}

func (b *ParseScope) transformInterfaceWithObject(e *types.Interface, named *types.Named, obj types.Object, others map[string]*Package) (Identity, error) {
	fmt.Printf("InterfaceWithObj: %#v -> %#v\n", e, e.Underlying())
	myPackage := obj.Pkg()
	targetPackage, ok := others[myPackage.Path()]
	if !ok {
		return nil, errors.Wrap(ErrNotFound, "Unable to find target package %q", obj.Pkg().Path())
	}

	ref := strings.Join([]string{targetPackage.Name, named.Obj().Name()}, ".")
	if target, ok := targetPackage.Interfaces[ref]; ok {
		return target, nil
	}

	imported := myPackage.Imports()
	for _, stage := range imported {
		targetPackage, ok := others[stage.Path()]
		if !ok {
			return nil, errors.Wrap(ErrNotFound, "Unable to find imported package %q", stage.Path())
		}

		ref := strings.Join([]string{targetPackage.Name, named.Obj().Name()}, ".")
		if target, ok := targetPackage.Interfaces[ref]; ok {
			return target, nil
		}
	}

	return nil, errors.Wrap(ErrNotFound, "Unable to find target struct %q from package or its imported set", named.Obj().Name())
}

func (b *ParseScope) transformFuncLit(e *ast.FuncLit, others map[string]*Package) (Identity, error) {
	fmt.Printf("Named: %#v -> %#v\n", e)
	return &Type{Methods: map[string]Function{}}, nil
}

func (b *ParseScope) transformCallExpr(e *ast.CallExpr, others map[string]*Package) (Identity, error) {
	fmt.Printf("Named: %#v -> %#v\n", e)
	return &Type{Methods: map[string]Function{}}, nil
}

func (b *ParseScope) transformEmptyStmt(e *ast.EmptyStmt, others map[string]*Package) (Identity, error) {
	fmt.Printf("Named: %#v -> %#v\n", e)
	return &Type{Methods: map[string]Function{}}, nil
}

func (b *ParseScope) transformBadStmt(e *ast.BadStmt, others map[string]*Package) (Identity, error) {
	fmt.Printf("Named: %#v -> %#v\n", e)
	return &Type{Methods: map[string]Function{}}, nil
}

func (b *ParseScope) transformExprStmt(e *ast.ExprStmt, others map[string]*Package) (Identity, error) {
	fmt.Printf("Named: %#v -> %#v\n", e)
	return &Type{Methods: map[string]Function{}}, nil
}

func (b *ParseScope) transformGoStmt(e *ast.GoStmt, others map[string]*Package) (Identity, error) {
	fmt.Printf("Named: %#v -> %#v\n", e)
	return &Type{Methods: map[string]Function{}}, nil
}

func (b *ParseScope) transformForStmt(e *ast.ForStmt, others map[string]*Package) (Identity, error) {
	fmt.Printf("Named: %#v -> %#v\n", e)
	return &Type{Methods: map[string]Function{}}, nil
}

func (b *ParseScope) transformIfStmt(e *ast.IfStmt, others map[string]*Package) (Identity, error) {
	fmt.Printf("Named: %#v -> %#v\n", e)
	return &Type{Methods: map[string]Function{}}, nil
}

func (b *ParseScope) transformIncDecStmt(e *ast.IncDecStmt, others map[string]*Package) (Identity, error) {
	fmt.Printf("Named: %#v -> %#v\n", e)
	return &Type{Methods: map[string]Function{}}, nil
}

func (b *ParseScope) transformIndexExpr(e *ast.IndexExpr, others map[string]*Package) (Identity, error) {
	fmt.Printf("Named: %#v -> %#v\n", e)
	return &Type{Methods: map[string]Function{}}, nil
}

func (b *ParseScope) transformKeyValueExpr(e *ast.KeyValueExpr, others map[string]*Package) (Identity, error) {
	fmt.Printf("Named: %#v -> %#v\n", e)
	return &Type{Methods: map[string]Function{}}, nil
}

func (b *ParseScope) transformLabeledStmt(e *ast.LabeledStmt, others map[string]*Package) (Identity, error) {
	fmt.Printf("Named: %#v -> %#v\n", e)
	return &Type{Methods: map[string]Function{}}, nil
}

func (b *ParseScope) transformParentExpr(e *ast.ParenExpr, others map[string]*Package) (Identity, error) {
	fmt.Printf("Named: %#v -> %#v\n", e)
	return &Type{Methods: map[string]Function{}}, nil
}

func (b *ParseScope) transformReturnStmt(e *ast.ReturnStmt, others map[string]*Package) (Identity, error) {
	fmt.Printf("Named: %#v -> %#v\n", e)
	return &Type{Methods: map[string]Function{}}, nil
}

func (b *ParseScope) transformSelectStmt(e *ast.SelectStmt, others map[string]*Package) (Identity, error) {
	fmt.Printf("Named: %#v -> %#v\n", e)
	return &Type{Methods: map[string]Function{}}, nil
}

func (b *ParseScope) transformSwitchStmt(e *ast.SwitchStmt, others map[string]*Package) (Identity, error) {
	fmt.Printf("Named: %#v -> %#v\n", e)
	return &Type{Methods: map[string]Function{}}, nil
}

func (b *ParseScope) transformSendStmt(e *ast.SendStmt, others map[string]*Package) (Identity, error) {
	fmt.Printf("Named: %#v -> %#v\n", e)
	return &Type{Methods: map[string]Function{}}, nil
}

func (b *ParseScope) transformTypeAssertStmt(e *ast.TypeAssertExpr, others map[string]*Package) (Identity, error) {
	fmt.Printf("Named: %#v -> %#v\n", e)
	return &Type{Methods: map[string]Function{}}, nil
}

func (b *ParseScope) transformTypeSwitchStmt(e *ast.TypeSwitchStmt, others map[string]*Package) (Identity, error) {
	fmt.Printf("Named: %#v -> %#v\n", e)
	return &Type{Methods: map[string]Function{}}, nil
}

func (b *ParseScope) transformRangeStmt(e *ast.RangeStmt, others map[string]*Package) (Identity, error) {
	fmt.Printf("Named: %#v -> %#v\n", e)
	return &Type{Methods: map[string]Function{}}, nil
}

func (b *ParseScope) transformUnaryExpr(e *ast.UnaryExpr, others map[string]*Package) (Identity, error) {
	fmt.Printf("Named: %#v -> %#v\n", e)
	return &Type{Methods: map[string]Function{}}, nil
}

func (b *ParseScope) transformEllipsis(e *ast.Ellipsis, others map[string]*Package) (Identity, error) {
	fmt.Printf("Named: %#v -> %#v\n", e)
	return &Type{Methods: map[string]Function{}}, nil
}

func (b *ParseScope) transformDeferStmt(e *ast.DeferStmt, others map[string]*Package) (Identity, error) {
	fmt.Printf("Named: %#v -> %#v\n", e)
	return &Type{Methods: map[string]Function{}}, nil
}

func (b *ParseScope) transformDeclrStmt(e *ast.DeclStmt, others map[string]*Package) (Identity, error) {
	fmt.Printf("Named: %#v -> %#v\n", e)
	return &Type{Methods: map[string]Function{}}, nil
}

func (b *ParseScope) transformCommClause(e *ast.CommClause, others map[string]*Package) (Identity, error) {
	fmt.Printf("Named: %#v -> %#v\n", e)
	return &Type{Methods: map[string]Function{}}, nil
}

func (b *ParseScope) transformCaseClause(e *ast.CaseClause, others map[string]*Package) (Identity, error) {
	fmt.Printf("Named: %#v -> %#v\n", e)
	return &Type{Methods: map[string]Function{}}, nil
}

func (b *ParseScope) transformAssignStmt(e *ast.AssignStmt, others map[string]*Package) (Identity, error) {
	fmt.Printf("Named: %#v -> %#v\n", e)
	return &Type{Methods: map[string]Function{}}, nil
}

func (b *ParseScope) transformBranchStmt(e *ast.BranchStmt, others map[string]*Package) (Identity, error) {
	fmt.Printf("Named: %#v -> %#v\n", e)
	return &Type{Methods: map[string]Function{}}, nil
}

func (b *ParseScope) transformBlockStatement(e *ast.BlockStmt, others map[string]*Package) (Identity, error) {
	fmt.Printf("Named: %#v -> %#v\n", e)
	return &Type{Methods: map[string]Function{}}, nil
}

func (b *ParseScope) transformBinaryExpr(e *ast.BinaryExpr, others map[string]*Package) (Identity, error) {
	fmt.Printf("Named: %#v -> %#v\n", e)
	return &Type{Methods: map[string]Function{}}, nil
}

func (b *ParseScope) transformStarExpr(e *ast.StarExpr, others map[string]*Package) (Identity, error) {
	fmt.Printf("Named: %#v -> %#v\n", e)
	return &Type{Methods: map[string]Function{}}, nil
}

func (b *ParseScope) transformArrayType(e *ast.ArrayType, others map[string]*Package) (Identity, error) {
	fmt.Printf("Named: %#v -> %#v\n", e)
	return &Type{Methods: map[string]Function{}}, nil
}

func (b *ParseScope) transformBasicLit(e *ast.BasicLit, others map[string]*Package) (Identity, error) {
	fmt.Printf("BasicLit: %#v -> %#v\n", e)
	return &Type{Methods: map[string]Function{}}, nil
}

func (b *ParseScope) transformCompositeLit(e *ast.CompositeLit, others map[string]*Package) (Identity, error) {
	fmt.Printf("Named: %#v -> %#v\n", e)
	return &Type{Methods: map[string]Function{}}, nil
}

func (b *ParseScope) transformChanType(e *ast.ChanType, others map[string]*Package) (Identity, error) {
	fmt.Printf("Named: %#v -> %#v\n", e)
	return &Type{Methods: map[string]Function{}}, nil
}

func (b *ParseScope) transformMapType(e *ast.MapType, others map[string]*Package) (Identity, error) {
	fmt.Printf("Named: %#v -> %#v\n", e)
	return &Type{Methods: map[string]Function{}}, nil
}

func (b *ParseScope) transformSliceExpr(e *ast.SliceExpr, others map[string]*Package) (Identity, error) {
	fmt.Printf("Named: %#v -> %#v\n", e)
	return &Type{Methods: map[string]Function{}}, nil
}

func (b *ParseScope) transformMethodSet(e *types.MethodSet, others map[string]*Package) (Identity, error) {
	fmt.Printf("Named: %#v -> %#v\n", e, e.Len())
	return &Type{Methods: map[string]Function{}}, nil
}

func (b *ParseScope) transformTypeAndValue(e *types.TypeAndValue, others map[string]*Package) (Identity, error) {
	fmt.Printf("Named: %#v -> %#v\n", e, e.Type)
	return &Type{Methods: map[string]Function{}}, nil
}

func (b *ParseScope) transformPointer(e *types.Pointer, others map[string]*Package) (Identity, error) {
	fmt.Printf("Named: %#v -> %#v\n", e, e.Underlying())
	return &Type{Methods: map[string]Function{}}, nil
}

func (b *ParseScope) transformSignature(signature *types.Signature, others map[string]*Package) (Function, error) {
	var fn Function

	if params := signature.Results(); params != nil {
		for i := 0; i < params.Len(); i++ {
			vard := params.At(i)
			ft, err := b.transformObject(vard, others)
			if err != nil {
				return fn, err
			}

			var field Parameter
			field.Type = ft
			field.Name = vard.Name()
			fn.Returns = append(fn.Returns, field)
		}
	}

	if params := signature.Params(); params != nil {
		for i := 0; i < params.Len(); i++ {
			vard := params.At(i)
			ft, err := b.transformObject(vard, others)
			if err != nil {
				return fn, err
			}

			var field Parameter
			field.Type = ft
			field.Name = vard.Name()
			fn.Returns = append(fn.Returns, field)
		}
	}

	return fn, nil
}

func (b *ParseScope) transformChanDir(e types.ChanDir, others map[string]*Package) (Identity, error) {
	fmt.Printf("Named: %#v -> %#v\n", e)
	return &Type{Methods: map[string]Function{}}, nil
}

func (b *ParseScope) transformChan(e *types.Chan, others map[string]*Package) (Identity, error) {
	fmt.Printf("Named: %#v -> %#v\n", e, e.Underlying())
	return &Type{Methods: map[string]Function{}}, nil
}

func (b *ParseScope) transformArray(e *types.Array, others map[string]*Package) (Identity, error) {
	fmt.Printf("Named: %#v -> %#v\n", e, e.Underlying())
	return &Type{Methods: map[string]Function{}}, nil
}

func (b *ParseScope) transformSlice(e *types.Slice, others map[string]*Package) (Identity, error) {
	fmt.Printf("Named: %#v -> %#v\n", e, e.Underlying())
	return &Type{Methods: map[string]Function{}}, nil
}

func (b *ParseScope) transformMap(e *types.Map, others map[string]*Package) (Identity, error) {
	fmt.Printf("Named: %#v -> %#v\n", e, e.Underlying())
	return &Type{Methods: map[string]Function{}}, nil
}

func (b *ParseScope) transformNamed(e *types.Named, others map[string]*Package) (Identity, error) {
	fmt.Printf("Named: %#v -> %#v\n", e.Obj(), e)
	return &Type{Methods: map[string]Function{}}, nil
}

func (b *ParseScope) transformStruct(e *types.Struct, others map[string]*Package) (Struct, error) {
	var declr Struct
	declr.Fields = map[string]Field{}

	for i := 0; i < e.NumFields(); i++ {
		field := e.Field(i)
		elem, err := b.transformObject(field, others)
		if err != nil {
			return declr, err
		}

		var fl Field
		fl.Type = elem
		fl.Name = field.Name()
		declr.Fields[fl.Name] = fl
	}

	return declr, nil
}

func (b *ParseScope) transformStructWithNamed(e *types.Struct, named *types.Named, others map[string]*Package) (Identity, error) {
	fmt.Printf("\nDO::Struct: %#v -> %#v\n\n", e, e.Underlying())
	var declr Struct
	return declr, nil
}

func (b *ParseScope) transformInterface(e *types.Interface, named *types.Named, others map[string]*Package) (Identity, error) {
	var declr Interface
	declr.Name = named.Obj().Name()
	declr.Methods = map[string]Function{}

	for i := 0; i < e.NumMethods(); i++ {
		method, err := b.transformFunc(e.Method(i), others)
		if err != nil {
			return declr, err
		}
		declr.Methods[method.ID()] = method.(Function)
	}

	// TODO(alex): We need to figure out the underline type
	// and sort this out.
	//for i := 0; i < e.NumEmbeddeds(); i++ {
	//	item := e.EmbeddedType(i)
	//	switch to := item.(type){
	//	case *types.Named:
	//	case *types.Interface:
	//		to.
	//	}
	//}

	return declr, nil
}

func (b *ParseScope) transformFunc(e *types.Func, others map[string]*Package) (Identity, error) {
	signature, ok := e.Type().(*types.Signature)
	if !ok {
		return nil, errors.New("expected signature as type: %#v", e.Type())
	}

	fn, err := b.transformSignature(signature, others)
	if err != nil {
		return nil, err
	}

	fn.Name = e.Name()
	return fn, nil
}

func (b *ParseScope) transformFuncType(e *ast.FuncType, others map[string]*Package) (Identity, error) {
	fmt.Printf("FuncType: %#v -> %#v\n", e, e)
	return &Type{Methods: map[string]Function{}}, nil
}

func (b *ParseScope) transformSelectorExprWithIdent(xident *ast.Ident, e *ast.SelectorExpr, others map[string]*Package) (Identity, error) {
	selObj := b.Info.ObjectOf(e.Sel)
	if selObj == nil {
		return nil, errors.Wrap(ErrNotFound, "unable to find object for selector %q", e.Sel.Name)
	}

	xObj := b.Info.ObjectOf(xident)
	if xObj == nil {
		return nil, errors.Wrap(ErrNotFound, "unable to find object for type %q", xident.Name)
	}

	meta, err := b.transformSelectExprToMeta(e, others)
	if err != nil {
		return nil, err
	}

	targetPackage, ok := others[meta.Path]
	if !ok {
		return nil, errors.Wrap(ErrNotFound, "unable to find package %q in indexed", meta.Path)
	}

	ref := strings.Join([]string{meta.Path, selObj.Name()}, ".")
	if target, ok := targetPackage.Interfaces[ref]; ok {
		return target, nil
	}

	if target, ok := targetPackage.Structs[ref]; ok {
		return target, nil
	}

	if target, ok := targetPackage.Types[ref]; ok {
		return target, nil
	}

	if target, ok := targetPackage.Functions[ref]; ok {
		return target, nil
	}

	return nil, errors.Wrap(ErrNotFound, "unable to find type for %q in %q", ref, meta.Path)
}

func (b *ParseScope) transformSelectorExprWithSelector(m *ast.SelectorExpr, e *ast.SelectorExpr, others map[string]*Package) (Identity, error) {
	fmt.Printf("First Selector: %#v\n", e)
	fmt.Printf("Second Selector: %#v\n", m)
	return nil, nil
}

func (b *ParseScope) transformSelectorExpr(e *ast.SelectorExpr, others map[string]*Package) (Identity, error) {
	switch tx := e.X.(type) {
	case *ast.Ident:
		return b.transformSelectorExprWithIdent(tx, e, others)
	case *ast.SelectorExpr:
		return b.transformSelectorExprWithSelector(tx, e, others)
	default:
		return nil, errors.New("unable to handle selector with type %#v", tx)
	}
}

func (b *ParseScope) transformSelectExprToMeta(e *ast.SelectorExpr, others map[string]*Package) (Meta, error) {
	var meta Meta
	xref, ok := e.X.(*ast.Ident)
	if !ok {
		return meta, errors.New("ast.SelectorExpr should have X as *ast.Ident")
	}

	meta.Name = xref.Name

	// We need to find specific import that uses giving name as import alias.
	imp, ok := b.Package.Imports[xref.Name]
	if !ok {
		return meta, errors.New("unable to find import with alias %q", xref.Name)
	}

	meta.Path = imp.Path
	return meta, nil
}

//******************************************************************************
//  Utilities
//******************************************************************************

var (
	varSignatureRegExp = regexp.MustCompile(`var\s([a-zA-Z0-9_]+)\s([a-zA-Z0-9_\.\/\$]+)`)
)

type varSignature struct {
	Name    string
	Package string
}

func getVarSignature(m string) (varSignature, error) {
	var sig varSignature
	parts := varSignatureRegExp.FindAllString(m, -1)
	if len(parts) < 3 {
		return sig, errors.New("no signature or invalid signature found: %q", m)
	}
	sig.Name = parts[1]
	sig.Package = parts[2]
	return sig, nil
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
