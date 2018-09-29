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

	indexer.waiter.Wait()

	// Get all indexed packages map.
	indexed := indexer.indexed

	// Call all indexed packages Resolve() method to ensure
	// all structures are adequately referenced.
	if err := pkg.Resolve(indexed); err != nil {
		return pkg, indexed, err
	}

	return pkg, indexed, nil
}

// indexPackage takes provided loader.Program returning a Package containing
// parsed structures, types and declarations of all packages.
func (indexer *Indexer) indexPackage(ctx context.Context, targetPackage string, p *loader.PackageInfo) (*Package, error) {
	pkg := &Package{
		Meta:             p,
		Name:             targetPackage,
		Types:            map[string]*Type{},
		Structs:          map[string]*Struct{},
		Variables:        map[string]*Variable{},
		Constants:        map[string]*Variable{},
		Functions:        map[string]*Function{},
		Methods:          map[string]*Function{},
		MethodByReceiver: map[string]*Function{},
		Interfaces:       map[string]*Interface{},
		Files:            map[string]*PackageFile{},
	}

	indexer.addIndexed(pkg)

	// send package has response after processing.
	return pkg, indexer.index(ctx, pkg, p)
}

func (indexer *Indexer) addIndexed(pkg *Package) {
	indexer.arw.Lock()
	indexer.indexed[pkg.Name] = pkg
	indexer.arw.Unlock()
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
	go func(p *Package) {
		defer indexer.waiter.Done()
		for elem := range in {
			if err := p.Add(elem); err != nil {
				log.Fatalf("Failed to add item into package: %+q", err)
			}
		}
	}(pkg)

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

var (
	pathRegEx      = regexp.MustCompile(`^([\da-z\.-]+\.[a-z\.]{2,6})([\/\w \.-]*)*\/?$`)
	pkgRegExp      = regexp.MustCompile(`\/([\w\d_-]+)\.(.+)$`)
	pkgRegExp2     = regexp.MustCompile(`^([\w\d]+)\.([\w\d-_]+)$`)
	typeNameRegExp = regexp.MustCompile(`^([\w\d_-]+)$`)
)

func (b *ParseScope) identityType(e typeRepresentation, others map[string]*Package) (typeIdentity, error) {
	var id typeIdentity

	typeString := e.String()
	if !pathRegEx.MatchString(typeString) && !pkgRegExp2.MatchString(typeString) && !typeNameRegExp.MatchString(typeString) {
		return id, errors.New("invalid data")
	}

	if typeNameRegExp.MatchString(typeString) {
		id.Type = typeString
		id.Text = typeString
		id.Alias = b.From.Name
		id.Package = b.From.Name
		return id, nil
	}

	if pkgRegExp2.MatchString(typeString) {
		target := pkgRegExp2.FindString(typeString)
		section := pkgRegExp2.FindAllStringSubmatch(target, -1)
		if len(section) == 0 {
			return id, errors.New("expected target regexp match")
		}

		matched := section[0]

		id.Text = target
		id.Type = matched[2]
		id.Alias = matched[1]
		id.Package = matched[1]
		return id, nil
	}

	target := pathRegEx.FindString(typeString)
	section := pkgRegExp.FindAllStringSubmatch(target, -1)

	if len(section) == 0 {
		return id, errors.New("expected target regexp match")
	}

	matched := section[0]

	id.Text = target
	id.Type = matched[2]
	id.Alias = matched[1]
	id.Package = strings.Replace(target, matched[0], "/"+matched[1], 1)
	return id, nil
}

type typeRepresentation interface {
	String() string
}

type typeIdentity struct {
	Package string
	Type    string
	Alias   string
	Text    string
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

		if imp.Path == "unsafe" {
			b.Indexer.addIndexed(unsafePackage)
			in <- unsafePackage
			continue
		}

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

func (b *ParseScope) handleVariable(ctx context.Context, mindex int, named *ast.Ident, val *ast.ValueSpec, spec ast.Spec, gen *ast.GenDecl, in chan interface{}) error {
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

	if val.Comment != nil {
		b.comments[val.Comment] = struct{}{}
		doc, cerr := b.handleCommentGroup(val.Comment)
		if cerr != nil {
			return cerr
		}
		declr.Docs = append(declr.Docs, doc)
	}

	obj := b.Info.ObjectOf(named)
	declr.Exported = obj.Exported()
	declr.Meta.Name = obj.Pkg().Name()
	declr.Meta.Path = obj.Pkg().Path()
	declr.Path = strings.Join([]string{obj.Pkg().Path(), named.Name}, ".")

	if _, ok := obj.(*types.Const); ok {
		declr.Constant = true
	}

	if len(val.Values) == 0 && val.Type == nil {
		declr.Type = AnyType
		in <- &declr
		return nil
	}

	declrAddr := &declr
	if len(val.Values) == 0 && val.Type != nil {
		declr.resolver = func(others map[string]*Package) error {
			vType, err := b.transformTypeFor(val.Type, others)
			if err != nil {
				return err
			}

			declrAddr.Type = vType
			return nil
		}
		in <- &declr
		return nil
	}

	var varValue ast.Expr
	if mindex < len(val.Values) {
		varValue = val.Values[mindex]
	}

	declr.resolver = func(others map[string]*Package) error {
		if val.Type != nil {
			vType, err := b.transformTypeFor(val.Type, others)
			if err != nil {
				return err
			}

			declrAddr.Type = vType
		}

		if varValue != nil {
			vValue, err := b.transformTypeFor(varValue, others)
			if err != nil {
				return err
			}

			if settable, ok := vValue.(SetIdentity); ok && vValue.ID() == "" {
				settable.SetID(named.Name)
			}

			declrAddr.Value = vValue
		}

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
	declr.Methods = map[string]*Function{}
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
			declr.Composes = append(declr.Composes, fl)
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

	declr.Methods = map[string]*Function{}
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
	declr.Name = ty.Name.Name
	declr.Methods = map[string]*Function{}
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
	declr.ScopeName = fn.Name.Name

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

		b.From.functionScope[declr.Path] = functionScope{
			Fn:    &declr,
			Scope: map[string]Identity{},
		}

		in <- &declr
		return nil
	}

	if len(fn.Recv.List) == 0 {
		declr.Path = strings.Join([]string{obj.Pkg().Path(), fn.Name.Name}, ".")

		b.From.functionScope[declr.Path] = functionScope{
			Fn:    &declr,
			Scope: map[string]Identity{},
		}

		in <- &declr
		return nil
	}

	target := fn.Recv.List[0]

	var instanceName *ast.Ident
	if len(target.Names) != 0 {
		instanceName = target.Names[0]
	} else {
		instanceName = &ast.Ident{Name: ""}
	}

	declr.ReceiverName = instanceName.Name

	switch head := target.Type.(type) {
	case *ast.StarExpr:
		headr, ok := head.X.(*ast.Ident)
		if !ok {
			return errors.New("ast.StarExpr does not use ast.Ident as X: %#v", head.X)
		}

		declr.IsPointerMethod = true
		declr.Path = strings.Join([]string{obj.Pkg().Path(), headr.Name, fn.Name.Name}, ".")
		declr.ReceiverAddr = strings.Join([]string{obj.Pkg().Path(), headr.Name, fn.Name.Name, instanceName.Name}, ".")
	case *ast.Ident:
		declr.Path = strings.Join([]string{obj.Pkg().Path(), head.Name, fn.Name.Name}, ".")
		declr.ReceiverAddr = strings.Join([]string{obj.Pkg().Path(), head.Name, fn.Name.Name, instanceName.Name}, ".")
	}

	fnAddr := &declr
	b.From.functionScope[declr.Path] = functionScope{
		Fn:    fnAddr,
		Scope: map[string]Identity{},
	}

	declr.resolver = func(others map[string]*Package) error {
		ownerType, err := b.getTypeFromFieldExpr(target.Type, target, others)
		if err != nil {
			return err
		}

		switch owner := ownerType.(type) {
		case *Struct:
			owner.Methods[declr.Name] = fnAddr
		case *Interface:
			owner.Methods[declr.Name] = fnAddr
		case *Type:
			owner.Methods[declr.Name] = fnAddr
		}

		fnAddr.IsMethod = true
		fnAddr.Owner = ownerType

		if fn.Body != nil {
			body, err := b.handleBlockStmt(fnAddr, fn.Body, others)
			if err != nil {
				return err
			}
			fnAddr.Body = body
		}

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

	if fn.Body == nil {
		return declr, nil
	}

	declrAddr := &declr
	declr.resolver = func(others map[string]*Package) error {
		body, err := b.handleBlockStmt(&declr, fn.Body, others)
		if err != nil {
			return err
		}
		declrAddr.Body = body
		return nil
	}

	b.From.deferedResolvers = append(b.From.deferedResolvers, declr.resolver)

	return declr, nil
}

func (b *ParseScope) handleFunctionType(name string, fn *ast.FuncType) (Function, error) {
	var declr Function
	declr.ScopeName = String(10)
	declr.Path = strings.Join([]string{b.From.Name, "func", declr.ScopeName}, ".")

	// read location information(line, column, source text etc) for giving type.
	declr.Location = b.getLocation(fn.Pos(), fn.End())

	b.From.functionScope[declr.Path] = functionScope{
		Fn:    &declr,
		Scope: map[string]Identity{},
	}

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

			params = append(params, *p)
			continue
		}

		// If we have name as a named Field or named return then
		// appropriately
		for _, name := range param.Names {
			p, err := b.handleFunctionFieldWithName(ownerName, param, name, param.Type)
			if err != nil {
				return params, err
			}

			params = append(params, *p)
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

	switch embed := identity.(type) {
	case *Interface:
		host.Composes[identity.ID()] = embed
	case Interface:
		host.Composes[identity.ID()] = &embed
	default:
		return errors.New("Expected type should be an Interface or *Interface: %#v\n", identity)
	}

	return nil
}

func (b *ParseScope) handleFunctionField(ownerName string, f *ast.Field, t ast.Expr) (*Function, error) {
	var p Function

	id, ok := t.(*ast.Ident)
	if !ok {
		return nil, errors.New("Expected *ast.Ident as ast.Expr for handleFunctionField")
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
			return nil, cerr
		}
		p.Docs = append(p.Docs, doc)
	}

	if f.Comment != nil {
		b.comments[f.Comment] = struct{}{}
		doc, cerr := b.handleCommentGroup(f.Comment)
		if cerr != nil {
			return nil, cerr
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

	return &p, nil
}

func (b *ParseScope) handleFunctionFieldWithName(ownerName string, f *ast.Field, nm *ast.Ident, t ast.Expr) (*Function, error) {
	fnType, ok := f.Type.(*ast.FuncType)
	if !ok {
		return nil, errors.New("failed to extract function type from Field")
	}

	p, err := b.handleFunctionType(nm.Name, fnType)
	if err != nil {
		return nil, err
	}

	p.Name = nm.Name

	if f.Doc != nil {
		b.comments[f.Doc] = struct{}{}
		doc, cerr := b.handleCommentGroup(f.Doc)
		if cerr != nil {
			return nil, err
		}
		p.Docs = append(p.Docs, doc)
	}

	if f.Comment != nil {
		b.comments[f.Comment] = struct{}{}
		doc, cerr := b.handleCommentGroup(f.Comment)
		if cerr != nil {
			return nil, err
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
			return nil, err
		}
	}

	if fnType.Results != nil {
		p.Returns, err = b.handleParameterList(nm.Name, fnType.Results)
		if err != nil {
			return nil, err
		}
	}

	return &p, nil
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
		pAddr.Name = tl.ID()
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
		pAddr.Name = tl.ID()
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
	base, err := b.transformTypeFor(e, others)
	if err != nil {
		return nil, err
	}
	return base, nil
}

func (b *ParseScope) getTypeFromFieldExpr(e ast.Expr, f *ast.Field, others map[string]*Package) (Identity, error) {
	base, err := b.transformTypeFor(e, others)
	if err != nil {
		return nil, err
	}
	return base, nil
}

//**********************************************************************************
// BlockStmt related transformations
//**********************************************************************************

func (b *ParseScope) handleBlockStmt(dn *Function, fn *ast.BlockStmt, others map[string]*Package) (*GroupStmt, error) {
	var gp GroupStmt
	gp.EndSymbol = "}"
	gp.BeginSymbol = "{"
	gp.Type = FunctionBody
	gp.Children = make([]Expr, 0, len(fn.List))

	// read location information(line, column, source text etc) for giving type.
	gp.Location = b.getLocation(fn.Pos(), fn.End())

	for _, block := range fn.List {
		stmt, err := b.transformStmt(dn, fn, block, others)
		if err != nil {
			return nil, err
		}
		gp.Children = append(gp.Children, stmt)
	}

	return &gp, nil
}

func (b *ParseScope) transformStmt(dn *Function, fn *ast.BlockStmt, block interface{}, others map[string]*Package) (Expr, error) {
	fmt.Printf("FuncBlock[%q:  %q]: %#v\n", b.Package.File, dn.Name, block)
	switch target := block.(type) {
	case *ast.SwitchStmt:
		return b.transformSwitchStatement(dn, fn, target, others)
	case *ast.AssignStmt:
		return b.transformAssignStmt(dn, fn, target, others)
	case *ast.BlockStmt:
		return b.transformBlockStmt(dn, fn, target, others)
	case *ast.EmptyStmt:
		return b.transformEmptyStmt(dn, fn, target, others)
	case *ast.BranchStmt:
		return b.transformBranchStmt(dn, fn, target, others)
	case *ast.DeferStmt:
		return b.transformDeferStmt(dn, fn, target, others)
	case *ast.DeclStmt:
		return b.transformDeclrStmt(dn, fn, target, others)
	case *ast.BadStmt:
		return b.transformBadStmt(dn, fn, target, others)
	case *ast.GoStmt:
		return b.transformGoStmt(dn, fn, target, others)
	case *ast.RangeStmt:
		return b.transformRangeStmt(dn, fn, target, others)
	case *ast.TypeSwitchStmt:
		return b.transformTypeSwitchStmt(dn, fn, target, others)
	case *ast.SendStmt:
		return b.transformSendStmt(dn, fn, target, others)
	case *ast.SelectStmt:
		return b.transformSelectStmt(dn, fn, target, others)
	case *ast.ReturnStmt:
		return b.transformReturnStmt(dn, fn, target, others)
	case *ast.LabeledStmt:
		return b.transformLabeledStmt(dn, fn, target, others)
	case *ast.IncDecStmt:
		return b.transformIncDecStmt(dn, fn, target, others)
	case *ast.IfStmt:
		return b.transformIfStmt(dn, fn, target, others)
	case *ast.ForStmt:
		return b.transformForStmt(dn, fn, target, others)
	case *ast.ExprStmt:
		return b.transformExprStmt(dn, fn, target, others)
	}

	return nil, errors.New("unable to handle giving Stmt %#v", block)
}

func (b *ParseScope) transformEmptyStmt(dn *Function, fn *ast.BlockStmt, sw *ast.EmptyStmt, others map[string]*Package) (Expr, error) {
	var stmt EmptyExpr
	stmt.Location = b.getLocation(sw.Pos(), sw.End())
	return &stmt, nil
}

func (b *ParseScope) transformExprStmt(dn *Function, fn *ast.BlockStmt, sw *ast.ExprStmt, others map[string]*Package) (Expr, error) {
	var stmt StmtExpr
	stmt.Location = b.getLocation(sw.Pos(), sw.End())

	return &stmt, nil
}

func (b *ParseScope) transformForStmt(dn *Function, fn *ast.BlockStmt, sw *ast.ForStmt, others map[string]*Package) (Expr, error) {
	var stmt ForExpr
	stmt.Location = b.getLocation(sw.Pos(), sw.End())

	return &stmt, nil
}

func (b *ParseScope) transformIfStmt(dn *Function, fn *ast.BlockStmt, sw *ast.IfStmt, others map[string]*Package) (Expr, error) {
	var stmt IfExpr
	stmt.Location = b.getLocation(sw.Pos(), sw.End())

	return &stmt, nil
}

func (b *ParseScope) transformIncDecStmt(dn *Function, fn *ast.BlockStmt, sw *ast.IncDecStmt, others map[string]*Package) (Expr, error) {
	var stmt IncDecExpr
	stmt.Location = b.getLocation(sw.Pos(), sw.End())

	return &stmt, nil
}

func (b *ParseScope) transformLabeledStmt(dn *Function, fn *ast.BlockStmt, sw *ast.LabeledStmt, others map[string]*Package) (Expr, error) {
	var stmt LabeledExpr
	stmt.Location = b.getLocation(sw.Pos(), sw.End())

	return &stmt, nil
}

func (b *ParseScope) transformReturnStmt(dn *Function, fn *ast.BlockStmt, sw *ast.ReturnStmt, others map[string]*Package) (Expr, error) {
	var stmt ReturnsExpr
	stmt.Location = b.getLocation(sw.Pos(), sw.End())

	return &stmt, nil
}

func (b *ParseScope) transformSelectStmt(dn *Function, fn *ast.BlockStmt, sw *ast.SelectStmt, others map[string]*Package) (Expr, error) {
	var stmt SelectExpr
	stmt.Location = b.getLocation(sw.Pos(), sw.End())

	return &stmt, nil
}

func (b *ParseScope) transformSendStmt(dn *Function, fn *ast.BlockStmt, sw *ast.SendStmt, others map[string]*Package) (Expr, error) {
	var stmt SendExpr
	stmt.Location = b.getLocation(sw.Pos(), sw.End())

	return &stmt, nil
}

func (b *ParseScope) transformTypeSwitchStmt(dn *Function, fn *ast.BlockStmt, sw *ast.TypeSwitchStmt, others map[string]*Package) (Expr, error) {
	var stmt TypeSwitchExpr
	stmt.Location = b.getLocation(sw.Pos(), sw.End())

	return &stmt, nil
}

func (b *ParseScope) transformRangeStmt(dn *Function, fn *ast.BlockStmt, sw *ast.RangeStmt, others map[string]*Package) (Expr, error) {
	var stmt RangeExpr
	stmt.Location = b.getLocation(sw.Pos(), sw.End())

	return &stmt, nil
}

func (b *ParseScope) transformDeclrStmt(dn *Function, fn *ast.BlockStmt, sw *ast.DeclStmt, others map[string]*Package) (Expr, error) {
	var stmt DeclrExpr
	stmt.Location = b.getLocation(sw.Pos(), sw.End())

	return &stmt, nil
}

func (b *ParseScope) transformGoStmt(dn *Function, fn *ast.BlockStmt, sw *ast.GoStmt, others map[string]*Package) (Expr, error) {
	var stmt GoExpr
	stmt.Location = b.getLocation(sw.Pos(), sw.End())

	return &stmt, nil
}

func (b *ParseScope) transformBadStmt(dn *Function, fn *ast.BlockStmt, sw *ast.BadStmt, others map[string]*Package) (Expr, error) {
	var stmt BadExpr
	stmt.Location = b.getLocation(sw.Pos(), sw.End())

	return &stmt, nil
}

func (b *ParseScope) transformDeferStmt(dn *Function, fn *ast.BlockStmt, sw *ast.DeferStmt, others map[string]*Package) (Expr, error) {
	var stmt DeferExpr
	stmt.Location = b.getLocation(sw.Pos(), sw.End())

	return &stmt, nil
}

func (b *ParseScope) transformBranchStmt(dn *Function, fn *ast.BlockStmt, sw *ast.BranchStmt, others map[string]*Package) (Expr, error) {
	var stmt BranchExpr
	stmt.Location = b.getLocation(sw.Pos(), sw.End())

	return &stmt, nil
}

func (b *ParseScope) transformAssignStmt(dn *Function, fn *ast.BlockStmt, sw *ast.AssignStmt, others map[string]*Package) (Expr, error) {
	var stmt AssignExpr
	stmt.Location = b.getLocation(sw.Pos(), sw.End())

	var leftHand []Identity
	//for _, left := range sw.Lhs {
	//	elem, err := b.transformTypeFor(left, others)
	//	if err != nil {
	//		return nil, err
	//	}
	//	leftHand = append(leftHand, elem)
	//}

	stmt.Left = leftHand

	var rightHand []Identity
	//for _, right := range sw.Rhs {
	//	elem, err := b.transformTypeFor(right, others)
	//	if err != nil {
	//		return nil, err
	//	}
	//	leftHand = append(leftHand, elem)
	//}

	stmt.Right = rightHand

	return &stmt, nil
}

func (b *ParseScope) transformBlockStmt(dn *Function, pa *ast.BlockStmt, sw *ast.BlockStmt, others map[string]*Package) (Expr, error) {
	var gp GroupStmt
	gp.EndSymbol = "}"
	gp.BeginSymbol = "{"
	gp.Type = StatementBlock
	gp.Children = make([]Expr, 0, len(sw.List))

	// read location information(line, column, source text etc) for giving type.
	gp.Location = b.getLocation(sw.Pos(), sw.End())

	for _, block := range sw.List {
		stmt, err := b.transformStmt(dn, sw, block, others)
		if err != nil {
			return nil, err
		}
		gp.Children = append(gp.Children, stmt)
	}

	return &gp, nil
}

func (b *ParseScope) transformSwitchStatement(dn *Function, fn *ast.BlockStmt, sw *ast.SwitchStmt, others map[string]*Package) (Expr, error) {
	var stmt SwitchExpr
	stmt.Location = b.getLocation(sw.Pos(), sw.End())

	return &stmt, nil
}

func (b *ParseScope) transformTypeForFunction(e interface{}, fn *Function, others map[string]*Package) (Identity, error) {

}

//**********************************************************************************
// Type related transformations
//**********************************************************************************

func (b *ParseScope) transformTypeFor(e interface{}, others map[string]*Package) (Identity, error) {
	switch core := e.(type) {
	case *ast.KeyValueExpr:
		return b.transformKeyValuePair(core, others)
	case *types.Var:
		return b.transformVarObject(core, others)
	case *ast.TypeAssertExpr:
		return b.transformTypeAssertExpr(core, others)
	case types.Object:
		return b.transformObject(core, others)
	case *ast.Ident:
		return b.transformIdent(core, others)
	case *types.Interface:
		return b.transformInterface(core, others)
	case *ast.InterfaceType:
		return b.transformInterfaceType(core, others)
	case *ast.StructType:
		return b.transformStructType(core, others)
	case *types.Struct:
		return b.transformStruct(core, others)
	case *ast.BasicLit:
		return b.transformBasicLitOnly(core, others)
	case *types.Basic:
		return b.transformBasic(core, others)
	case *ast.BinaryExpr:
		return b.transformBinaryExpr(core, others)
	case *ast.UnaryExpr:
		return b.transformUnaryExpr(core, others)
	case *ast.ParenExpr:
		return b.transformParenExpr(core, others)
	case *ast.IndexExpr:
		return b.transformIndexExpr(core, others)
	case *ast.CallExpr:
		return b.transformCallExpr(core, others)
	case *ast.StarExpr:
		return b.transformStarExpr(core, others)
	case *ast.SelectorExpr:
		return b.transformSelectorExpr(core, others)
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
	case *ast.CompositeLit:
		return b.transformCompositeLitValue(core, others)
	case *ast.MapType:
		return b.transformMapType(core, others)
	case *types.Map:
		return b.transformMap(core, others)
	case *ast.ChanType:
		return b.transformChanType(core, others)
	case *types.Chan:
		return b.transformChan(core, others)
	case *types.Signature:
		return b.transformSignature(core, others)
	case *ast.FuncType:
		return b.transformFuncType(core, others)
	}

	return nil, errors.Wrap(ErrNotFound, "unable to handle type: %#v", e)
}

func (b *ParseScope) transformTypeAssertExpr(src *ast.TypeAssertExpr, others map[string]*Package) (Identity, error) {
	var ts TypeAssert

	// read location information(line, column, source text etc) for giving type.
	ts.Location = b.getLocation(src.Pos(), src.End())

	xt, err := b.transformTypeFor(src.X, others)
	if err != nil {
		return nil, err
	}

	ts.X = xt

	tt, err := b.transformTypeFor(src.Type, others)
	if err != nil {
		return nil, err
	}

	ts.Type = tt

	return ts, nil
}

func (b *ParseScope) transformKeyValuePair(src *ast.KeyValueExpr, others map[string]*Package) (Identity, error) {
	var pair KeyValuePair

	// read location information(line, column, source text etc) for giving type.
	pair.Location = b.getLocation(src.Pos(), src.End())

	nkey, err := b.transformTypeFor(src.Key, others)
	if err != nil {
		return nil, err
	}

	pair.Key = nkey

	value, err := b.transformTypeFor(src.Value, others)
	if err != nil {
		return nil, err
	}

	pair.Value = value

	return &pair, nil
}

func (b *ParseScope) transformUnaryExpr(src *ast.UnaryExpr, others map[string]*Package) (Identity, error) {
	base, err := b.transformTypeFor(src.X, others)
	if err != nil {
		return nil, err
	}

	if src.Op != token.AND {
		var op OpOf
		op.Elem = base
		op.OpFlag = int(src.Op)
		op.Op = src.Op.String()
		op.Location = b.getLocation(src.Pos(), src.End())

		return &op, nil
	}

	var val AddressOf
	val.Elem = base
	val.Name = base.ID()

	// read location information(line, column, source text etc) for giving type.
	val.Location = b.getLocation(src.Pos(), src.End())

	return &val, nil
}

func (b *ParseScope) transformBinaryExpr(src *ast.BinaryExpr, others map[string]*Package) (Identity, error) {
	var bin BinaryExpr
	bin.Op = src.Op.String()

	leftHand, err := b.transformTypeFor(src.X, others)
	if err != nil {
		return nil, err
	}

	rightHand, err := b.transformTypeFor(src.Y, others)
	if err != nil {
		return nil, err
	}

	bin.Left = leftHand
	bin.Right = rightHand
	return &bin, nil
}

func (b *ParseScope) transformParenExpr(src *ast.ParenExpr, others map[string]*Package) (Identity, error) {
	var bin ParenExpr
	elem, err := b.transformTypeFor(src.X, others)
	if err != nil {
		return nil, err
	}
	bin.Elem = elem
	return &bin, nil
}

func (b *ParseScope) transformIndexExpr(src *ast.IndexExpr, others map[string]*Package) (*IndexExpr, error) {
	var val IndexExpr
	val.Location = b.getLocation(src.Pos(), src.End())

	elem, err := b.transformTypeFor(src.X, others)
	if err != nil {
		return nil, err
	}
	val.Elem = elem

	switch id := src.Index.(type) {
	case *ast.BasicLit:
		val.Index = id.Value
	default:
		return nil, errors.New("unable to handle index type: %#v\n", id)
	}

	return &val, nil
}

func (b *ParseScope) transformCallExpr(src *ast.CallExpr, others map[string]*Package) (*CallExpr, error) {
	var val CallExpr
	val.Location = b.getLocation(src.Pos(), src.End())

	fn, err := b.transformTypeFor(src.Fun, others)
	if err != nil {
		return nil, err
	}

	val.Func = fn

	for _, item := range src.Args {
		used, err := b.transformTypeFor(item, others)
		if err != nil {
			return nil, err
		}
		val.Arguments = append(val.Arguments, used)
	}

	return &val, nil
}

func (b *ParseScope) transformBasicLitOnly(e *ast.BasicLit, others map[string]*Package) (Identity, error) {
	var bm BaseValue
	bm.Value = e.Value
	bm.Location = b.getLocation(e.Pos(), e.End())
	return bm, nil
}

func (b *ParseScope) getBaseType(name string) (Identity, error) {
	switch name {
	case "error":
		return ErrorType, nil
	case "interface{}", "interface":
		return AnyType, nil
	case "Type1", "Integer1", "Type":
		return BaseFor(name), nil
	case "IntegerType", "FloatType", "ComplexType":
		return BaseFor(name), nil
	case "bool":
		return BaseFor(name), nil
	case "uintptr", "uint", "int", "int8", "int16", "int32", "int64", "uint8", "uint16", "uint32", "uint64":
		return BaseFor(name), nil
	case "rune", "string":
		return BaseFor(name), nil
	case "float32", "float64":
		return BaseFor(name), nil
	case "complex128", "complex64":
		return BaseFor(name), nil
	}
	return nil, errors.New("unknown base type %q", name)
}

func (b *ParseScope) transformIdent(e *ast.Ident, others map[string]*Package) (Identity, error) {
	if bd, err := b.getBaseType(e.Name); err == nil {
		return bd, err
	}

	if e.Obj == nil {
		bm := BaseFor(e.Name)
		bm.Location = b.getLocation(e.Pos(), e.End())
		return bm, nil
	}

	bo := b.Info.ObjectOf(e)
	to := bo.Type()
	if identity, err := b.identityType(to, others); err == nil {
		bm, err := b.locateReferencedIdentity(identity, others)
		return bm, err
	}

	ref := strings.Join([]string{b.From.Name, e.Name}, ".")
	if b, err := b.findWithinPackage(ref, b.From); err == nil {
		return b, nil
	}

	return BaseFor(e.Name), nil
}

func (b *ParseScope) transformVarObject(src *types.Var, others map[string]*Package) (Identity, error) {
	if src.Pkg() != nil {
		if ref, err := b.locateRefFromPackage(src.Name(), src.Pkg().Path(), others); err == nil {
			return ref, nil
		}
	}

	if ref, err := b.locateRefFromPackage(src.Name(), b.From.Name, others); err == nil {
		return ref, nil
	}

	return nil, errors.New("unable to locate var object: %#v", src)
}

func (b *ParseScope) transformAssignedValueSpec(spec *ast.ValueSpec, src *ast.Ident, others map[string]*Package) (Identity, error) {
	if len(spec.Names) == 0 {
		return nil, errors.New("ast.ValueSpec.Names must have atleast a item in list")
	}

	var targetIndex int
	var targetIdent *ast.Ident

	for index, name := range spec.Names {
		if name.Name != src.Name {
			continue
		}
		targetIdent = name
		targetIndex = index
		break
	}

	if targetIdent == nil {
		return nil, errors.New("ast.ValueSpec has not correlative index with target ast.Ident %#v", src)
	}

	targetValue := spec.Values[targetIndex]
	return b.transformTypeFor(targetValue, others)
}

func (b *ParseScope) transformCompositeLitValue(src *ast.CompositeLit, others map[string]*Package) (Identity, error) {
	if src.Type == nil {
		return b.transformNoTypeComposite(src, others)
	}

	switch elem := src.Type.(type) {
	case *ast.Ident:
		return b.transformIdentComposite(elem, src, others)
	case *ast.SelectorExpr:
		return b.transformSelectorComposite(elem, src, others)
	case *ast.StructType:
		return b.transformStructComposite(elem, src, others)
	case *ast.ArrayType:
		return b.transformArrayComposite(elem, src, others)
	case *ast.MapType:
		return b.transformMapComposite(elem, src, others)
	}
	return nil, errors.New("unable to handle ast.Composite type %T %#v", src.Type, src)
}

func (b *ParseScope) transformMapTypeComposite(tl *types.Map, src *ast.CompositeLit, others map[string]*Package) (*DeclaredMapValue, error) {
	var val DeclaredMapValue
	val.Location = b.getLocation(src.Pos(), src.End())

	if len(src.Elts) == 0 {
		return &val, nil
	}

	for _, elem := range src.Elts {
		kv, ok := elem.(*ast.KeyValueExpr)
		if !ok {
			return nil, errors.New("CompositeLit for Map type must have KeyValueExpr as children")
		}

		pk, err := b.transformTypeFor(kv.Key, others)
		if err != nil {
			return nil, err
		}

		pv, err := b.transformTypeFor(kv.Value, others)
		if err != nil {
			return nil, err
		}

		val.KeyValues = append(val.KeyValues, KeyValuePair{Key: pk, Value: pv})
	}

	return &val, nil
}

func (b *ParseScope) transformMapComposite(tl *ast.MapType, src *ast.CompositeLit, others map[string]*Package) (*DeclaredMapValue, error) {
	var val DeclaredMapValue
	val.Location = b.getLocation(src.Pos(), src.End())

	if len(src.Elts) == 0 {
		return &val, nil
	}

	for _, elem := range src.Elts {
		kv, ok := elem.(*ast.KeyValueExpr)
		if !ok {
			return nil, errors.New("CompositeLit for Map type must have KeyValueExpr as children")
		}

		pk, err := b.transformTypeFor(kv.Key, others)
		if err != nil {
			return nil, err
		}

		pv, err := b.transformTypeFor(kv.Value, others)
		if err != nil {
			return nil, err
		}

		val.KeyValues = append(val.KeyValues, KeyValuePair{Key: pk, Value: pv})
	}

	return &val, nil
}

func (b *ParseScope) transformNoTypeComposite(src *ast.CompositeLit, others map[string]*Package) (Identity, error) {
	var val DeclaredValue
	val.Location = b.getLocation(src.Pos(), src.End())

	if len(src.Elts) == 0 {
		return &val, nil
	}

	for _, elem := range src.Elts {
		item, err := b.transformTypeFor(elem, others)
		if err != nil {
			return nil, err
		}
		val.Values = append(val.Values, item)
	}

	return &val, nil
}

func (b *ParseScope) transformArrayComposite(tl *ast.ArrayType, src *ast.CompositeLit, others map[string]*Package) (*DeclaredListValue, error) {
	var val DeclaredListValue
	val.Location = b.getLocation(src.Pos(), src.End())

	if len(src.Elts) == 0 {
		return &val, nil
	}

	for _, elem := range src.Elts {
		item, err := b.transformTypeFor(elem, others)
		if err != nil {
			return nil, err
		}
		val.Values = append(val.Values, item)
	}

	return &val, nil
}

func (b *ParseScope) transformSliceTypeComposite(tl *types.Slice, src *ast.CompositeLit, others map[string]*Package) (*DeclaredListValue, error) {
	var val DeclaredListValue
	val.Location = b.getLocation(src.Pos(), src.End())

	if len(src.Elts) == 0 {
		return &val, nil
	}

	for _, elem := range src.Elts {
		item, err := b.transformTypeFor(elem, others)
		if err != nil {
			return nil, err
		}
		val.Values = append(val.Values, item)
	}

	return &val, nil
}

func (b *ParseScope) transformArrayTypeComposite(tl *types.Array, src *ast.CompositeLit, others map[string]*Package) (*DeclaredListValue, error) {
	var val DeclaredListValue
	val.Length = tl.Len()
	val.Location = b.getLocation(src.Pos(), src.End())

	if len(src.Elts) == 0 {
		return &val, nil
	}

	for _, elem := range src.Elts {
		item, err := b.transformTypeFor(elem, others)
		if err != nil {
			return nil, err
		}
		val.Values = append(val.Values, item)
	}

	return &val, nil
}

func (b *ParseScope) transformStructTypeComposite(tl *types.Struct, src *ast.CompositeLit, others map[string]*Package) (*DeclaredStructValue, error) {
	var val DeclaredStructValue
	val.Fields = map[string]Identity{}
	val.Location = b.getLocation(src.Pos(), src.End())

	if len(src.Elts) == 0 {
		return &val, nil
	}

	for _, elem := range src.Elts {
		switch belem := elem.(type) {
		case *ast.BasicLit:
			val.ValuesOnly = true
			member, err := b.transformTypeFor(belem, others)
			if err != nil {
				return nil, err
			}
			val.Values = append(val.Values, member)
		case *ast.KeyValueExpr:
			fieldName, ok := belem.Key.(*ast.Ident)
			if !ok {
				return nil, errors.New("ast.KeyValueExpr should be a *ast.Ident: %#v", belem.Key)
			}

			fieldVal, err := b.transformTypeFor(belem.Value, others)
			if err != nil {
				return nil, err
			}

			val.Fields[fieldName.Name] = fieldVal
		}
	}

	return &val, nil
}

func (b *ParseScope) transformStructComposite(tl *ast.StructType, src *ast.CompositeLit, others map[string]*Package) (*DeclaredStructValue, error) {
	var val DeclaredStructValue
	val.Fields = map[string]Identity{}
	val.Location = b.getLocation(src.Pos(), src.End())

	if len(src.Elts) == 0 {
		return &val, nil
	}

	for _, elem := range src.Elts {
		switch belem := elem.(type) {
		case *ast.BasicLit:
			val.ValuesOnly = true
			member, err := b.transformTypeFor(belem, others)
			if err != nil {
				return nil, err
			}
			val.Values = append(val.Values, member)
		case *ast.KeyValueExpr:
			fieldName, ok := belem.Key.(*ast.Ident)
			if !ok {
				return nil, errors.New("ast.KeyValueExpr should be a *ast.Ident: %#v", belem.Key)
			}

			fieldVal, err := b.transformTypeFor(belem.Value, others)
			if err != nil {
				return nil, err
			}

			val.Fields[fieldName.Name] = fieldVal
		}
	}

	return &val, nil
}

func (b *ParseScope) transformIdentComposite(tl *ast.Ident, src *ast.CompositeLit, others map[string]*Package) (Identity, error) {
	identObj := b.Info.ObjectOf(tl)
	identObjType, ok := identObj.Type().(*types.Named)
	if !ok {
		return nil, errors.New("ast.Ident for IdentComposite should have types.Named as type")
	}

	switch base := identObjType.Underlying().(type) {
	case *types.Map:
		return b.transformMapTypeComposite(base, src, others)
	case *types.Array:
		return b.transformArrayTypeComposite(base, src, others)
	case *types.Slice:
		return b.transformSliceTypeComposite(base, src, others)
	case *types.Struct:
		return b.transformStructTypeComposite(base, src, others)
	}

	return nil, errors.New("types.Ident types.Object.Underlying() for ast.CompositeLit unable to be handled: %#v", identObjType.Underlying())
}

func (b *ParseScope) transformSelectorComposite(tl *ast.SelectorExpr, src *ast.CompositeLit, others map[string]*Package) (Identity, error) {
	selObj := b.Info.ObjectOf(tl.Sel)
	selType, ok := selObj.Type().(*types.Named)
	if !ok {
		return nil, errors.New("ast.SelectorExpr for SelectorComposite should have types.Named as type")
	}

	switch base := selType.Underlying().(type) {
	case *types.Map:
		return b.transformMapTypeComposite(base, src, others)
	case *types.Slice:
		return b.transformSliceTypeComposite(base, src, others)
	case *types.Struct:
		return b.transformStructTypeComposite(base, src, others)
	}

	return nil, errors.New("types.SelectorExpr types.Object.Underlying() for ast.CompositeLit unable to be handled: %#v", selType.Underlying())
}

func (b *ParseScope) transformBasic(e *types.Basic, others map[string]*Package) (Base, error) {
	return BaseFor(e.Name()), nil
}

func (b *ParseScope) transformObject(e types.Object, others map[string]*Package) (Identity, error) {
	// Is this a package-less type, if so then we need to handle type.
	if e.Pkg() == nil {
		return b.transformPackagelessObject(e.Type(), e, others)
	}

	switch tm := e.Type().(type) {
	case *types.Map:
		return b.transformMapWithObject(tm, e, others)
	case *types.Chan:
		return b.transformChanWithObject(tm, e, others)
	case *types.Array:
		return b.transformArrayWithObject(tm, e, others)
	case *types.Slice:
		return b.transformSliceWithObject(tm, e, others)
	case *types.Basic:
		return b.transformBasicWithObject(tm, e, others)
	case *types.Struct:
		return b.transformStructWithObject(tm, e, others)
	case *types.Interface:
		return b.transformInterfaceWithObject(tm, e, others)
	case *types.Signature:
		return b.transformSignatureWithObject(tm, e, others)
	case *types.Named:
		return b.transformNamedWithObject(tm, e, others)
	case *types.Pointer:
		return b.transformPointerWithObject(tm, e, others)
	}

	return nil, errors.New("unable to transform object implementation: %#v", e)
}

func (b *ParseScope) transformMapWithObject(e *types.Map, vard types.Object, others map[string]*Package) (Map, error) {
	var declr Map

	if _, ok := e.Underlying().(*types.Pointer); ok {
		declr.IsPointer = true
	}

	var err error
	declr.KeyType, err = b.transformTypeFor(e.Key(), others)
	if err != nil {
		return declr, err
	}

	declr.ValueType, err = b.transformTypeFor(e.Elem(), others)
	if err != nil {
		return declr, err
	}

	return declr, nil
}

func (b *ParseScope) transformPointerWithObject(e *types.Pointer, vard types.Object, others map[string]*Package) (Identity, error) {
	var err error
	var declr Pointer
	declr.Elem, err = b.transformTypeFor(e.Elem(), others)
	return declr, err
}

func (b *ParseScope) transformArrayWithObject(e *types.Array, vard types.Object, others map[string]*Package) (List, error) {
	var declr List
	if _, ok := e.Elem().(*types.Pointer); ok {
		declr.IsPointer = true
	}

	var err error
	declr.Type, err = b.transformTypeFor(e.Elem(), others)
	return declr, err
}

func (b *ParseScope) transformSliceWithObject(e *types.Slice, vard types.Object, others map[string]*Package) (List, error) {
	var declr List
	declr.IsSlice = true

	if _, ok := e.Elem().(*types.Pointer); ok {
		declr.IsPointer = true
	}

	var err error
	declr.Type, err = b.transformTypeFor(e.Elem(), others)
	return declr, err
}

func (b *ParseScope) transformChanWithObject(e *types.Chan, vard types.Object, others map[string]*Package) (Channel, error) {
	return b.transformChan(e, others)
}

func (b *ParseScope) transformBasicWithObject(e *types.Basic, vard types.Object, others map[string]*Package) (Base, error) {
	return b.transformBasic(e, others)
}

func (b *ParseScope) transformInterfaceWithObject(e *types.Interface, vard types.Object, others map[string]*Package) (Identity, error) {
	if identity, err := b.identityType(e, others); err == nil {
		return b.locateReferencedIdentity(identity, others)
	}
	return b.transformTypeFor(e, others)
}

func (b *ParseScope) transformStructWithObject(e *types.Struct, vard types.Object, others map[string]*Package) (Identity, error) {
	if identity, err := b.identityType(e, others); err == nil {
		return b.locateReferencedIdentity(identity, others)
	}
	return b.transformTypeFor(e, others)
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
		return b.transformInterfaceWithNamed(tm, e, others)
	}
	return nil, errors.New("package-less named type: %#v", e)
}

func (b *ParseScope) transformNamedWithObject(e *types.Named, obj types.Object, others map[string]*Package) (Identity, error) {
	if identity, err := b.identityType(e, others); err == nil {
		return b.locateReferencedIdentity(identity, others)
	}

	switch tm := e.Underlying().(type) {
	case *types.Struct:
		return b.locateStructWithObject(tm, e, obj, others)
	case *types.Interface:
		return b.locateInterfaceWithObject(tm, e, obj, others)
	case *types.Slice:
		return b.transformTypeFor(tm, others)
	case *types.Map:
		return b.transformTypeFor(tm, others)
	case *types.Array:
		return b.transformTypeFor(tm, others)
	case *types.Chan:
		return b.transformTypeFor(tm, others)
	case *types.Basic:
		return b.transformTypeFor(tm, others)
	}
	return nil, errors.New("unknown named type with object: %#v", e)
}

func (b *ParseScope) transformNamed(e *types.Named, others map[string]*Package) (Identity, error) {
	if identity, err := b.identityType(e, others); err == nil {
		return b.locateReferencedIdentity(identity, others)
	}
	return b.transformTypeFor(e.Underlying(), others)
}

func (b *ParseScope) transformSignatureWithObject(signature *types.Signature, obj types.Object, others map[string]*Package) (Function, error) {
	var fn Function
	fn.IsVariadic = signature.Variadic()

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

func (b *ParseScope) locateStructWithObject(e *types.Struct, named *types.Named, obj types.Object, others map[string]*Package) (Identity, error) {
	if identity, err := b.identityType(e, others); err == nil {
		return b.locateReferencedIdentity(identity, others)
	}

	if named.Obj().Pkg() == nil {
		return b.transformPackagelessObject(named, named.Obj(), others)
	}

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
		targetPackage, ok := others[stage.Path()]
		if !ok {
			return nil, errors.Wrap(ErrNotFound, "Unable to find imported package %q", stage.Path())
		}

		ref := strings.Join([]string{targetPackage.Name, named.Obj().Name()}, ".")
		if target, ok := targetPackage.Structs[ref]; ok {
			return target, nil
		}
	}

	return nil, errors.Wrap(ErrNotFound, "Unable to find target struct %q from package %q or its imported set", named.Obj().Name(), named.Obj().Pkg().Path())
}

func (b *ParseScope) locateReferencedIdentity(location typeIdentity, others map[string]*Package) (Identity, error) {
	if bd, err := b.getBaseType(location.Text); err == nil {
		return bd, err
	}

	targetPackage, ok := others[location.Package]
	if !ok {
		return nil, errors.Wrap(ErrNotFound, "unable to find package for %q", location.Package)
	}

	if target, ok := targetPackage.Interfaces[location.Text]; ok {
		return target, nil
	}

	if target, ok := targetPackage.Structs[location.Text]; ok {
		return target, nil
	}

	if target, ok := targetPackage.Types[location.Text]; ok {
		return target, nil
	}

	if target, ok := targetPackage.Functions[location.Text]; ok {
		return target, nil
	}

	if target, ok := targetPackage.Methods[location.Text]; ok {
		return target, nil
	}

	if target, ok := targetPackage.Variables[location.Text]; ok {
		return target, nil
	}

	if target, ok := targetPackage.Constants[location.Text]; ok {
		return target, nil
	}

	return nil, errors.Wrap(ErrNotFound, "unable to find type for %q in %q", location.Type, location.Package)
}

func (b *ParseScope) locateInterfaceWithObject(e *types.Interface, named *types.Named, obj types.Object, others map[string]*Package) (Identity, error) {
	if identity, err := b.identityType(e, others); err == nil {
		return b.locateReferencedIdentity(identity, others)
	}

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

	if named.Obj().Pkg() == nil {
		return b.transformPackagelessObject(named, named.Obj(), others)
	}

	return nil, errors.Wrap(ErrNotFound, "Unable to find target Interface %q from package %q or its imported set", named.Obj().Name(), named.Obj().Pkg().Path())
}

func (b *ParseScope) transformStructWithNamed(e *types.Struct, named *types.Named, others map[string]*Package) (Identity, error) {
	if identity, err := b.identityType(e, others); err == nil {
		return b.locateReferencedIdentity(identity, others)
	}
	return b.transformTypeFor(e, others)
}

func (b *ParseScope) transformInterfaceWithNamed(e *types.Interface, named *types.Named, others map[string]*Package) (Identity, error) {
	if identity, err := b.identityType(e, others); err == nil {
		return b.locateReferencedIdentity(identity, others)
	}

	var declr Interface
	declr.Name = named.Obj().Name()
	declr.Methods = map[string]*Function{}

	for i := 0; i < e.NumMethods(); i++ {
		method, err := b.transformFunc(e.Method(i), others)
		if err != nil {
			return declr, err
		}
		declr.Methods[method.ID()] = method
	}

	for i := 0; i < e.NumEmbeddeds(); i++ {
		item := e.EmbeddedType(i)
		emb, err := b.transformTypeFor(item, others)
		if err != nil {
			return declr, err
		}

		if iemb, ok := emb.(*Interface); ok {
			declr.Composes[iemb.Addr()] = iemb
		}

		if iemb, ok := emb.(Interface); ok {
			declr.Composes[iemb.Addr()] = &iemb
		}
	}

	return declr, nil
}

func (b *ParseScope) transformFuncLit(e *ast.FuncLit, others map[string]*Package) (Function, error) {
	return b.handleFunctionLit(e)
}

func (b *ParseScope) transformInterfaceType(e *ast.InterfaceType, others map[string]*Package) (*Interface, error) {
	var declr Interface
	declr.Methods = map[string]*Function{}

	// read location information(line, column, source text etc) for giving type.
	declr.Location = b.getLocation(e.Pos(), e.End())

	if e.Methods == nil {
		return &declr, nil
	}

	for _, field := range e.Methods.List {
		if len(field.Names) == 0 {
			if err := b.handleEmbeddedInterface("interface", field, field.Type, others, &declr); err != nil {
				return nil, err
			}
			continue
		}

		for _, name := range field.Names {
			fn, err := b.handleFunctionFieldWithName("interface", field, name, field.Type)
			if err != nil {
				return nil, err
			}
			declr.Methods[fn.Name] = fn
		}
	}

	if err := declr.Resolve(others); err != nil {
		return nil, err
	}

	return &declr, nil
}

func (b *ParseScope) transformStructType(e *ast.StructType, others map[string]*Package) (*Struct, error) {
	var declr Struct
	declr.Fields = map[string]Field{}

	// read location information(line, column, source text etc) for giving type.
	declr.Location = b.getLocation(e.Pos(), e.End())

	if e.Fields == nil {
		return &declr, nil
	}

	for _, field := range e.Fields.List {
		if len(field.Names) == 0 {
			parsedField, err := b.handleField("struct", field, field.Type)
			if err != nil {
				return nil, err
			}

			if err := parsedField.Resolve(others); err != nil {
				return nil, err
			}

			declr.Composes = append(declr.Composes, parsedField)
			continue
		}

		for _, fieldName := range field.Names {
			parsedField, err := b.handleFieldWithName("struct", field, fieldName, field.Type)
			if err != nil {
				return nil, err
			}

			if err := parsedField.Resolve(others); err != nil {
				return nil, err
			}

			declr.Fields[parsedField.Name] = parsedField
		}
	}

	if err := declr.Resolve(others); err != nil {
		return nil, err
	}

	return &declr, nil
}

func (b *ParseScope) transformStarExpr(e *ast.StarExpr, others map[string]*Package) (DePointer, error) {
	var err error
	var declr DePointer

	// read location information(line, column, source text etc) for giving type.
	declr.Location = b.getLocation(e.Pos(), e.End())

	declr.Elem, err = b.transformTypeFor(e.X, others)
	return declr, err
}

func (b *ParseScope) transformArrayType(e *ast.ArrayType, others map[string]*Package) (List, error) {
	var declr List
	if _, ok := e.Elt.(*ast.StarExpr); ok {
		declr.IsPointer = true
	}

	// read location information(line, column, source text etc) for giving type.
	declr.Location = b.getLocation(e.Pos(), e.End())

	var err error
	declr.Type, err = b.transformTypeFor(e.Elt, others)
	return declr, err
}

func (b *ParseScope) transformChanType(e *ast.ChanType, others map[string]*Package) (Channel, error) {
	var err error
	var declr Channel

	// read location information(line, column, source text etc) for giving type.
	declr.Location = b.getLocation(e.Pos(), e.End())

	declr.Type, err = b.transformTypeFor(e.Value, others)
	return declr, err
}

func (b *ParseScope) transformMapType(e *ast.MapType, others map[string]*Package) (Map, error) {
	var declr Map

	var err error
	declr.KeyType, err = b.transformTypeFor(e.Key, others)
	if err != nil {
		return declr, err
	}

	declr.ValueType, err = b.transformTypeFor(e.Value, others)
	if err != nil {
		return declr, err
	}

	return declr, nil
}

func (b *ParseScope) transformPointer(e *types.Pointer, others map[string]*Package) (Pointer, error) {
	var err error
	var declr Pointer
	declr.Elem, err = b.transformTypeFor(e.Elem(), others)
	return declr, err
}

func (b *ParseScope) transformSignature(signature *types.Signature, others map[string]*Package) (*Function, error) {
	var fn Function
	fn.IsVariadic = signature.Variadic()

	if params := signature.Results(); params != nil {
		for i := 0; i < params.Len(); i++ {
			vard := params.At(i)
			ft, err := b.transformObject(vard, others)
			if err != nil {
				return nil, err
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
				return nil, err
			}

			var field Parameter
			field.Type = ft
			field.Name = vard.Name()
			fn.Returns = append(fn.Returns, field)
		}
	}

	return &fn, nil
}

func (b *ParseScope) transformChan(e *types.Chan, others map[string]*Package) (Channel, error) {
	var err error
	var declr Channel
	declr.Type, err = b.transformTypeFor(e.Elem(), others)
	return declr, err
}

func (b *ParseScope) transformArray(e *types.Array, others map[string]*Package) (Identity, error) {
	var declr List
	if _, ok := e.Elem().(*types.Pointer); ok {
		declr.IsPointer = true
	}

	var err error
	declr.Type, err = b.transformTypeFor(e.Elem(), others)
	return declr, err
}

func (b *ParseScope) transformSlice(e *types.Slice, others map[string]*Package) (Identity, error) {
	var declr List
	declr.IsSlice = true

	if _, ok := e.Elem().(*types.Pointer); ok {
		declr.IsPointer = true
	}

	var err error
	declr.Type, err = b.transformTypeFor(e.Elem(), others)
	return declr, err
}

func (b *ParseScope) transformMap(e *types.Map, others map[string]*Package) (Identity, error) {
	var declr Map

	var err error
	declr.KeyType, err = b.transformTypeFor(e.Key(), others)
	if err != nil {
		return declr, err
	}

	declr.ValueType, err = b.transformTypeFor(e.Elem(), others)
	if err != nil {
		return declr, err
	}

	return declr, nil
}

func (b *ParseScope) transformStruct(e *types.Struct, others map[string]*Package) (Identity, error) {
	if identity, err := b.identityType(e, others); err == nil {
		return b.locateReferencedIdentity(identity, others)
	}

	var declr Struct
	declr.Fields = map[string]Field{}

	for i := 0; i < e.NumFields(); i++ {
		field := e.Field(i)
		elem, err := b.transformObject(field, others)
		if err != nil {
			return declr, err
		}

		var fl Field
		fl.Tags = GetTags(e.Tag(i))
		fl.Type = elem
		fl.Name = field.Name()
		declr.Fields[fl.Name] = fl
	}

	if err := declr.Resolve(others); err != nil {
		return nil, err
	}

	return &declr, nil
}

func (b *ParseScope) transformInterface(e *types.Interface, others map[string]*Package) (Identity, error) {
	if identity, err := b.identityType(e, others); err == nil {
		return b.locateReferencedIdentity(identity, others)
	}

	var declr Interface
	declr.Methods = map[string]*Function{}

	for i := 0; i < e.NumMethods(); i++ {
		method, err := b.transformFunc(e.Method(i), others)
		if err != nil {
			return declr, err
		}
		declr.Methods[method.ID()] = method
	}

	for i := 0; i < e.NumEmbeddeds(); i++ {
		item := e.EmbeddedType(i)
		emb, err := b.transformTypeFor(item, others)
		if err != nil {
			return nil, err
		}

		if iemb, ok := emb.(*Interface); ok {
			declr.Composes[iemb.Addr()] = iemb
		}

		if iemb, ok := emb.(Interface); ok {
			declr.Composes[iemb.Addr()] = &iemb
		}
	}

	if err := declr.Resolve(others); err != nil {
		return nil, err
	}

	return &declr, nil
}

func (b *ParseScope) transformFunc(e *types.Func, others map[string]*Package) (*Function, error) {
	signature, ok := e.Type().(*types.Signature)
	if !ok {
		return nil, errors.New("expected *types.Signature as type: %#v", e.Type())
	}

	fn, err := b.transformSignature(signature, others)
	if err != nil {
		return nil, err
	}

	fn.Name = e.Name()
	fn.Exported = e.Exported()
	return fn, nil
}

func (b *ParseScope) transformFuncType(e *ast.FuncType, others map[string]*Package) (*Function, error) {
	var declr Function

	// read location information(line, column, source text etc) for giving type.
	declr.Location = b.getLocation(e.Pos(), e.End())

	if e.Params != nil {
		var params []Parameter
		for _, param := range e.Params.List {
			if len(param.Names) == 0 {
				p, err := b.handleParameter("func", param, param.Type)
				if err != nil {
					return nil, err
				}

				if err = p.Resolve(others); err != nil {
					return nil, err
				}

				params = append(params, p)
				continue
			}

			// If we have name as a named parameter or named return then
			// appropriately
			for _, name := range param.Names {
				p, err := b.handleParameterWithName("func", param, name, param.Type)
				if err != nil {
					return nil, err
				}

				if err = p.Resolve(others); err != nil {
					return nil, err
				}

				params = append(params, p)
			}
		}

		declr.Arguments = params
	}

	if e.Results != nil {
		var params []Parameter
		for _, param := range e.Params.List {
			if len(param.Names) == 0 {
				p, err := b.handleParameter("func", param, param.Type)
				if err != nil {
					return nil, err
				}

				if err = p.Resolve(others); err != nil {
					return nil, err
				}

				params = append(params, p)
				continue
			}

			// If we have name as a named parameter or named return then
			// appropriately
			for _, name := range param.Names {
				p, err := b.handleParameterWithName("func", param, name, param.Type)
				if err != nil {
					return nil, err
				}

				if err = p.Resolve(others); err != nil {
					return nil, err
				}

				params = append(params, p)
			}
		}

		declr.Returns = params
	}

	return &declr, nil
}

func (b *ParseScope) transformSelectorExpr(parent *ast.SelectorExpr, others map[string]*Package) (Identity, error) {
	switch tx := parent.X.(type) {
	case *ast.CompositeLit:
		return b.transformSelectorWithComposite(tx, parent, others)
	case *ast.Ident:
		return b.transformSelectorExprWithIdent(tx, parent, others)
	case *ast.SelectorExpr:
		return b.transformSelectorExprWithSelector(tx, parent, others)
	}
	return nil, errors.New("unable to handle selector with type %#v", parent.X)
}

func (b *ParseScope) transformSelectExprToMeta(e *ast.SelectorExpr, others map[string]*Package) (Meta, error) {
	var meta Meta
	xref, ok := e.X.(*ast.Ident)
	if !ok {
		return meta, errors.New("ast.SelectorExpr should have X as *ast.Ident")
	}

	meta.Name = xref.Name

	// Are we dealing with a package import?
	imp, ok := b.Package.Imports[xref.Name]
	if !ok {
		return meta, errors.New("unable to find import with alias %q", xref.Name)
	}

	meta.Path = imp.Path
	return meta, nil
}

func (b *ParseScope) transformSelectorWithComposite(cp *ast.CompositeLit, e *ast.SelectorExpr, others map[string]*Package) (Identity, error) {
	switch baseTarget := cp.Type.(type) {
	case *ast.Ident:
		element, err := b.transformTypeFor(baseTarget, others)
		if err != nil {
			return nil, err
		}

		return b.locateRefFromObject([]string{e.Sel.Name}, element, others)
	case *ast.SelectorExpr:
		fmt.Printf("Attempting to target another selector: %#v\n", baseTarget)
		element, err := b.transformTypeFor(baseTarget, others)
		if err != nil {
			return nil, err
		}

		return b.locateRefFromObject([]string{e.Sel.Name}, element, others)
	case *ast.StructType:
		tStruct, err := b.transformTypeFor(baseTarget, others)
		if err != nil {
			return nil, err
		}

		if tss, ok := tStruct.(Struct); ok {
			if err := tss.Resolve(others); err != nil {
				return nil, err
			}
		}

		if tss, ok := tStruct.(Struct); ok {
			if err := tss.Resolve(others); err != nil {
				return nil, err
			}
		}

		if err := b.From.Add(tStruct); err != nil {
			return nil, err
		}

		return b.locateRefFromObject([]string{e.Sel.Name}, tStruct, others)
	}

	return nil, errors.New("failed to handle ast.SelectorExpr with ast.CompositeLit: %#v : %#v", e, cp.Type)
}

func (b *ParseScope) transformSelectorExprWithIdent(me *ast.Ident, e *ast.SelectorExpr, others map[string]*Package) (Identity, error) {
	//Are we dealing with an import prefix?
	if meta, err := b.transformSelectExprToMeta(e, others); err == nil {
		return b.locateRefFromPackage(e.Sel.Name, meta.Path, others)
	}

	// Are we dealing with a internal variable or declared type
	if me.Obj != nil {
		if parent, err := b.getElementFromPackageWithObject(me); err == nil {
			return b.locateRefFromObject([]string{e.Sel.Name}, parent, others)
		}
	}

	fmt.Printf("Next::SelectorProcessing[%q][%q] -> %#v\n", b.From.Name, me.Name, e.Sel)

	fmt.Printf("Selector[%q][%q] -> %#v -> %q\n", b.From.Name, me.Name, me.Obj, e.Sel)

	//if targetRef, err := b.findWithinPackage(ref, b.From); err == nil {
	//	return b.locateRefFromObject([]string{selObj.Name()}, targetRef, others)
	//}

	return nil, errors.New("unable to find selector target: %q", me.Name)
}

func (b *ParseScope) getElementFromPackageWithObject(target *ast.Ident) (Identity, error) {
	ref := strings.Join([]string{b.From.Name, target.Name}, ".")
	switch tl := target.Obj.Decl.(type) {
	case *ast.ValueSpec:
		if found, ok := b.From.Variables[ref]; ok {
			return found, nil
		}
	case *ast.TypeSpec:
		switch tl.Type.(type) {
		case *ast.InterfaceType:
			if found, ok := b.From.Interfaces[ref]; ok {
				return found, nil
			}
		case *ast.StructType:
			if found, ok := b.From.Structs[ref]; ok {
				return found, nil
			}
		default:
			if found, ok := b.From.Types[ref]; ok {
				return found, nil
			}

			if found, ok := b.From.Functions[ref]; ok {
				return found, nil
			}
		}
	}
	return nil, errors.New("unable to handle underline declareation type")
}

func (b *ParseScope) transformSelectorExprWithSelector(next *ast.SelectorExpr, parent *ast.SelectorExpr, others map[string]*Package) (Identity, error) {
	parts, err := b.getSelectorTree(nil, next, parent, others)
	if err != nil {
		return nil, err
	}

	// We need to find specific import that uses giving name as import alias.
	if imp, ok := b.Package.Imports[parts[0]]; ok {
		return b.locateRefFromPathSeries(parts[1:], imp.Path, others)
	}

	// Are we dealing with an internal package object (variable, struct, interface, function) ?
	ref := strings.Join([]string{b.From.Name, parts[0]}, ".")
	if targetRef, err := b.findWithinPackage(ref, b.From); err == nil {
		return b.locateRefFromObject(parts[1:], targetRef, others)
	}

	return nil, errors.New("unable to find import with alias %q", parts[0])
}

func (b *ParseScope) getSelectorTree(parts []string, next *ast.SelectorExpr, parent *ast.SelectorExpr, others map[string]*Package) ([]string, error) {
	if parent.Sel != nil {
		parts = append(parts, parent.Sel.Name)
	}

	parts = append(parts, next.Sel.Name)

	if next.X != nil {
		if nextSel, ok := next.X.(*ast.SelectorExpr); ok {
			return b.getSelectorTree(parts, nextSel, next, others)
		}
	}

	nextIdent, ok := next.X.(*ast.Ident)
	if !ok {
		return parts, errors.New("expected ast.Selector.X value to be ast.Ident for last one")
	}

	// finally add the target name of expected next selector as the ast is done reverse.
	parts = append(parts, nextIdent.Name)

	if len(parts) == 2 {
		tmp := parts[0]
		parts[0] = parts[1]
		parts[1] = tmp
		return parts, nil
	}

	partLen := len(parts)
	partRLen := partLen - 1
	middle := partLen / 2
	for i := 0; i < middle; i++ {
		base := parts[i]
		radi := parts[partRLen-i]
		parts[i] = radi
		parts[partRLen-i] = base
	}

	return parts, nil
}

func (b *ParseScope) locateRefFromPathSeries(parts []string, pkg string, others map[string]*Package) (Identity, error) {
	base, err := b.locateRefFromPackage(parts[0], pkg, others)
	if err != nil {
		return nil, err
	}

	return b.locateRefFromObject(parts[1:], base, others)
}

func (b *ParseScope) locateRefFromObject(parts []string, target Identity, others map[string]*Package) (Identity, error) {
	if target == nil {
		return nil, errors.New("target can not be nil")
	}

	if len(parts) == 0 {
		return target, nil
	}

	switch item := target.(type) {
	case Field:
		return b.locateRefFromObject(parts[1:], item.Type, others)
	case *Field:
		return b.locateRefFromObject(parts[1:], item.Type, others)
	case *Interface:
		if fn, ok := item.Methods[parts[0]]; ok {
			return b.locateRefFromObject(parts[1:], fn, others)
		}
		return nil, errors.New("unable to locate giving method from interface: %+q", parts)
	case *Struct:
		if fn, ok := item.Fields[parts[0]]; ok {
			return b.locateRefFromObject(parts[1:], fn, others)
		}
		if fn, ok := item.Methods[parts[0]]; ok {
			return b.locateRefFromObject(parts[1:], fn, others)
		}
		for _, fl := range item.Composes {
			if fl.Name != parts[0] {
				continue
			}
			return b.locateRefFromObject(parts[1:], &fl, others)
		}
		for name, fl := range item.Embeds {
			if name != parts[0] {
				continue
			}
			return b.locateRefFromObject(parts[1:], fl, others)
		}
		return nil, errors.New("unable to locate giving method or field from struct: %+q", parts)
	case *Type:
		if fn, ok := item.Methods[parts[0]]; ok {
			return b.locateRefFromObject(parts[1:], fn, others)
		}
		return nil, errors.New("unable to locate giving method from named type: %+q", parts)
	case *Variable:
		return b.locateRefFromObject(parts[1:], item.Type, others)
	}

	return nil, errors.New("unable to locate any of provided set %+q in %T", parts, target)
}

func (b *ParseScope) locateRefFromPackage(target string, pkg string, others map[string]*Package) (Identity, error) {
	if targetPackage, ok := others[pkg]; ok {
		ref := strings.Join([]string{pkg, target}, ".")
		return b.findWithinPackage(ref, targetPackage)
	}

	return nil, errors.Wrap(ErrNotFound, "unable to find package %q in indexed", pkg)
}

func (b *ParseScope) findWithinPackage(ref string, targetPackage *Package) (Identity, error) {
	if target, ok := targetPackage.Variables[ref]; ok {
		return target, nil
	}

	if target, ok := targetPackage.Constants[ref]; ok {
		return target, nil
	}

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

	if target, ok := targetPackage.Methods[ref]; ok {
		return target, nil
	}

	return nil, errors.Wrap(ErrNotFound, "unable to find type for %q in %q", ref, targetPackage.Name)
}
