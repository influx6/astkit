package compiler

import (
	"bytes"
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

const (
	blankIdentifier = "_"
)

var (
	buffers = sync.Pool{
		New: func() interface{} {
			return bytes.NewBuffer(make([]byte, 0, 512))
		},
	}
)

// Indexer implements a golang ast index which parses
// a loader.Program returning a Package containing all
// definitions.
type Indexer struct {
	C *Cg

	waiter  sync.WaitGroup
	arw     sync.RWMutex
	indexed map[string]*Package
}

// NewIndexer returns a new instance of an Indexer.
func NewIndexer(c Cg) *Indexer {
	return &Indexer{
		C:       &c,
		indexed: map[string]*Package{},
	}
}

// PreloadedIndexer returns a new instance of an Indexer.
func PreloadedIndexer(c Cg, preload map[string]*Package) *Indexer {
	return &Indexer{
		C:       &c,
		indexed: preload,
	}
}

// Load is the central function used for processing a giving package with current architecture, platform
// configuration and other desired achitectures.
func (indexer *Indexer) Index(ctx context.Context, pkg string) (*Package, map[string]*Package, error) {
	if err := indexer.C.init(); err != nil {
		return nil, nil, err
	}

	pkgd, err := indexer.indexArchAndPlatform(ctx, pkg, indexer.C.GoArch, indexer.C.GOOS)
	if err != nil {
		return nil, nil, err
	}

	for platform, archs := range indexer.C.ExtraArchs {
		for _, arch := range archs {
			if arch == indexer.C.GoArch && platform == indexer.C.GOOS {
				continue
			}

			if _, err := indexer.indexArchAndPlatform(ctx, pkg, arch, platform); err != nil {
				if indexer.C.PlatformError != nil {
					indexer.C.PlatformError(err, arch, platform)
				}
			}
		}
	}

	indexer.waiter.Wait()

	// Resolve all package dependencies with
	// indexed map of all packages.
	indexed := indexer.indexed
	if err := pkgd.Resolve(indexed); err != nil {
		return pkgd, indexed, err
	}

	return pkgd, indexed, nil
}

// indexArchAndPlatform takes provided loader.Program returning a Package containing
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
func (indexer *Indexer) indexArchAndPlatform(ctx context.Context, basePackage string, arch string, goos string) (*Package, error) {
	program, err := Load(indexer.C, basePackage, arch, goos)
	if err != nil {
		return nil, err
	}

	pkg, err := indexer.indexPackage(ctx, program, basePackage)
	if err != nil {
		return nil, err
	}

	return pkg, nil
}

// indexPackage takes provided loader.Program returning a Package containing
// parsed structures, types and declarations of all packages.
func (indexer *Indexer) indexPackage(ctx context.Context, program *loader.Program, targetPackage string) (*Package, error) {
	var pkg *Package

	if prevPackage := indexer.getIndexed(targetPackage); prevPackage != nil {
		pkg = prevPackage
	} else {
		pkg = &Package{
			Name:             targetPackage,
			CgoPackages:      NewArch(targetPackage),
			NormalPackages:   NewArch(targetPackage),
			Depends:          map[string]*Package{},
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
	}

	info := program.Package(targetPackage)
	if info == nil {
		return nil, errors.New("Package %q data not available", targetPackage)
	}

	if err := indexer.indexProgram(ctx, program, pkg, info); err != nil {
		return pkg, err
	}

	return pkg, nil
}

// index runs logic on a per package basis handling all commentaries, type declarations which
// which be processed and added into provided package reference.
func (indexer *Indexer) indexProgram(ctx context.Context, program *loader.Program, pkg *Package, p *loader.PackageInfo) error {
	in := make(chan interface{}, 0)
	w, ctx := errgroup.WithContext(ctx)

	// Run through all files for package and schedule them to appropriately
	// send
	for _, file := range p.Files {
		filePos := program.Fset.Position(file.Pos())

		// if the file was already processed, then skip.
		if _, ok := pkg.Files[filePos.Filename]; ok {
			continue
		}

		if err := indexer.indexFile(ctx, w, in, program, pkg, file, p); err != nil {
			return err
		}
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

// indexFile runs logic on a per package basis handling all commentaries, type declarations which
// which be processed and added into provided package reference.
func (indexer *Indexer) indexFile(ctx context.Context, eg *errgroup.Group, in chan interface{}, program *loader.Program, pkg *Package, file *ast.File, p *loader.PackageInfo) error {
	pkgFile := new(PackageFile)
	_, begin, end := compiler.GetPosition(program.Fset, file.Pos(), file.End())
	if begin.IsValid() {
		pkgFile.File = filepath.ToSlash(begin.Filename)
	} else if end.IsValid() {
		pkgFile.File = filepath.ToSlash(begin.Filename)
	}

	platform, arch := getPlatformArchitecture(filepath.Base(pkgFile.File))

	pkgFile.Name = filepath.Base(pkgFile.File)
	pkgFile.Dir = filepath.ToSlash(filepath.Dir(pkgFile.File))

	pkgFile.Archs = map[string]bool{}
	if arch != "" {
		pkgFile.Archs[arch] = true
	}

	pkgFile.Platforms = map[string]bool{}
	if platform != "" {
		pkgFile.Platforms[platform] = true
	}

	pkg.Files[pkgFile.File] = pkgFile

	var scope ParseScope
	scope.Info = p
	scope.From = pkg
	scope.File = file
	scope.Indexer = indexer
	scope.Package = pkgFile
	scope.Program = program
	scope.scopes = map[string]FunctionScope{}
	scope.parentScopes = map[string]FunctionScope{}

	// if file has an associated package-level documentation,
	// parse it and add to package documentation.
	if file.Doc != nil {
		doc, err := scope.handleCommentGroup(file.Doc)
		if err != nil {
			return err
		}

		pkg.Docs = append(pkg.Docs, doc)
	}

	eg.Go(func() error {
		return scope.Parse(ctx, in)
	})

	return nil
}

// indexImported runs indexing as a fresh package on giving import path, if path is found,
// then a new indexing is spurned and returns a new Package instance at the end else
// returning an error. This exists to allow abstracting the indexing process using
// the import path as target, because of the initial logic for Indexer, as we need
// to be able to skip already indexed packages or register index packages, more so
// if a package already has being indexed, it is just returned.
func (indexer *Indexer) indexImported(ctx context.Context, program *loader.Program, path string) (*Package, error) {
	indexer.arw.RLock()
	if pkg, ok := indexer.indexed[path]; ok {
		indexer.arw.RUnlock()
		return pkg, nil
	}
	indexer.arw.RUnlock()

	// Have imported package indexed so we can add into
	// dependency map and send pkg pointer to root.
	return indexer.indexPackage(ctx, program, path)
}

func (indexer *Indexer) getIndexed(pkgName string) *Package {
	indexer.arw.Lock()
	defer indexer.arw.Unlock()
	if pkgd, ok := indexer.indexed[pkgName]; ok {
		return pkgd
	}
	return nil
}

func (indexer *Indexer) addIndexed(pkg *Package) {
	indexer.arw.Lock()
	indexer.indexed[pkg.Name] = pkg
	indexer.arw.Unlock()
}

//******************************************************************************
// Type ast.Visitor Implementations
//******************************************************************************

// ParseScope defines a struct which embodies a current parsing scope
// related to a giving file, package and program.
type ParseScope struct {
	SkipProcessed bool
	From          *Package
	File          *ast.File
	Indexer       *Indexer
	Package       *PackageFile
	Program       *loader.Program
	Info          *loader.PackageInfo
	scopes        map[string]FunctionScope
	parentScopes  map[string]FunctionScope
	comments      map[*ast.CommentGroup]struct{}
}

// Parse runs through all non-comment based declarations within the
// giving file.
func (b *ParseScope) Parse(ctx context.Context, in chan interface{}) error {
	if b.comments == nil {
		b.comments = map[*ast.CommentGroup]struct{}{}
	}

	b.handleBuildCommentary()

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
				if err := b.handleDeclarations(spec, elem, in); err != nil {
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

func (b *ParseScope) identityType(e typeRepresentation, others map[string]*Package) (typeExpr, error) {
	var id typeExpr

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

type typeExpr struct {
	Package string
	Type    string
	Alias   string
	Text    string
}

func (b *ParseScope) handleDeclarations(spec ast.Spec, gen *ast.GenDecl, in chan interface{}) error {
	switch obj := spec.(type) {
	case *ast.ValueSpec:
		if err := b.handleVariables(obj, spec, gen, in); err != nil {
			return err
		}
	case *ast.TypeSpec:
		switch ty := obj.Type.(type) {
		case *ast.InterfaceType:
			if err := b.handleInterface(ty, obj, spec, gen, in); err != nil {
				return err
			}
		case *ast.StructType:
			if err := b.handleStruct(ty, obj, spec, gen, in); err != nil {
				return err
			}
		default:
			if err := b.handleNamedType(obj, spec, gen, in); err != nil {
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
		pkg, err := b.Indexer.indexImported(ctx, b.Program, imp.Path)
		if err != nil {
			return err
		}

		in <- pkg
	}
	return nil
}

func (b *ParseScope) handleVariables(val *ast.ValueSpec, spec ast.Spec, gen *ast.GenDecl, in chan interface{}) error {
	for index, named := range val.Names {
		if err := b.handleVariable(index, named, val, spec, gen, in); err != nil {
			return err
		}
	}
	return nil
}

func (b *ParseScope) handleVariable(mindex int, named *ast.Ident, val *ast.ValueSpec, spec ast.Spec, gen *ast.GenDecl, in chan interface{}) error {
	declr, err := b.transformVariable(mindex, named, val, spec, gen)
	if err != nil {
		return err
	}

	in <- declr
	return nil
}

func (b *ParseScope) transformVariable(mindex int, named *ast.Ident, val *ast.ValueSpec, spec ast.Spec, gen *ast.GenDecl) (*Variable, error) {
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
			return nil, err
		}

		declr.Doc = doc

		buff := buffers.Get().(*bytes.Buffer)
		buff.WriteString(val.Doc.Text())
		if annons := ParseAnnotationFromReader(buff); len(annons) != 0 {
			declr.Annotations = append(declr.Annotations, annons...)
		}

		buff.Reset()
		buffers.Put(buff)
	}

	if val.Comment != nil {
		b.comments[val.Comment] = struct{}{}
		doc, cerr := b.handleCommentGroup(val.Comment)
		if cerr != nil {
			return nil, cerr
		}
		declr.Docs = append(declr.Docs, doc)

		buff := buffers.Get().(*bytes.Buffer)
		buff.WriteString(val.Comment.Text())
		if annons := ParseAnnotationFromReader(buff); len(annons) != 0 {
			declr.Annotations = append(declr.Annotations, annons...)
		}

		buff.Reset()
		buffers.Put(buff)
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
		return &declr, nil
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

		return &declr, nil
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

	return &declr, nil
}

func (b *ParseScope) handleStruct(str *ast.StructType, ty *ast.TypeSpec, spec ast.Spec, gen *ast.GenDecl, in chan interface{}) error {
	declr, err := b.transformStructDeclr(str, ty, spec, gen)
	if err != nil {
		return err
	}

	in <- declr
	return nil
}

func (b *ParseScope) transformStructDeclr(str *ast.StructType, ty *ast.TypeSpec, spec ast.Spec, gen *ast.GenDecl) (*Struct, error) {
	var declr Struct

	var structName string
	if ty.Name != nil {
		structName = ty.Name.Name
	} else {
		structName = "struct"
	}

	declr.Name = structName
	declr.Fields = map[string]*Field{}
	declr.Embeds = map[string]*Struct{}
	declr.Methods = map[string]*Function{}
	declr.Location = b.getLocation(ty.Pos(), ty.End())

	if gen.Doc != nil {
		b.comments[gen.Doc] = struct{}{}
		doc, err := b.handleCommentGroup(gen.Doc)
		if err != nil {
			return nil, err
		}

		declr.Doc = doc

		buff := buffers.Get().(*bytes.Buffer)
		buff.WriteString(gen.Doc.Text())

		if annons := ParseAnnotationFromReader(buff); len(annons) != 0 {
			declr.Annotations = append(declr.Annotations, annons...)
		}

		buff.Reset()
		buffers.Put(buff)
	}

	if ty.Doc != nil && ty.Doc != gen.Doc {
		b.comments[ty.Doc] = struct{}{}
		doc, err := b.handleCommentGroup(ty.Doc)
		if err != nil {
			return nil, err
		}

		declr.Docs = append(declr.Docs, doc)

		buff := buffers.Get().(*bytes.Buffer)
		buff.WriteString(ty.Doc.Text())
		if annons := ParseAnnotationFromReader(buff); len(annons) != 0 {
			declr.Annotations = append(declr.Annotations, annons...)
		}

		buff.Reset()
		buffers.Put(buff)
	}

	if ty.Comment != nil {
		b.comments[ty.Comment] = struct{}{}
		doc, err := b.handleCommentGroup(ty.Comment)
		if err != nil {
			return nil, err
		}

		declr.Docs = append(declr.Docs, doc)

		buff := buffers.Get().(*bytes.Buffer)
		buff.WriteString(ty.Comment.Text())
		if annons := ParseAnnotationFromReader(buff); len(annons) != 0 {
			declr.Annotations = append(declr.Annotations, annons...)
		}

		buff.Reset()
		buffers.Put(buff)
	}

	obj := b.Info.ObjectOf(ty.Name)
	declr.Exported = obj.Exported()
	declr.Meta.Name = obj.Pkg().Name()
	declr.Meta.Path = obj.Pkg().Path()
	declr.Path = strings.Join([]string{obj.Pkg().Path(), structName}, ".")

	for _, field := range str.Fields.List {
		if len(field.Names) == 0 {
			fl, err := b.handleField(structName, field, field.Type)
			if err != nil {
				return nil, err
			}

			declr.Composes = append(declr.Composes, fl)
			continue
		}

		for _, name := range field.Names {
			fl, err := b.handleFieldWithName(structName, field, name, field.Type)
			if err != nil {
				return nil, err
			}
			declr.Fields[fl.Name] = fl
		}
	}

	return &declr, nil
}

func (b *ParseScope) handleInterface(str *ast.InterfaceType, ty *ast.TypeSpec, spec ast.Spec, gen *ast.GenDecl, in chan interface{}) error {
	declr, err := b.transformInterfaceSpec(str, ty, spec, gen)
	if err != nil {
		return err
	}

	in <- declr
	return nil
}

func (b *ParseScope) transformInterfaceSpec(str *ast.InterfaceType, ty *ast.TypeSpec, spec ast.Spec, gen *ast.GenDecl) (*Interface, error) {
	var declr Interface
	declr.Name = ty.Name.Name

	declr.Methods = map[string]*Function{}
	declr.Composes = map[string]*Interface{}
	declr.Location = b.getLocation(ty.Pos(), ty.End())

	if gen.Doc != nil {
		b.comments[gen.Doc] = struct{}{}
		doc, err := b.handleCommentGroup(gen.Doc)
		if err != nil {
			return nil, err
		}

		declr.Doc = doc

		buff := buffers.Get().(*bytes.Buffer)
		buff.WriteString(gen.Doc.Text())

		if annons := ParseAnnotationFromReader(buff); len(annons) != 0 {
			declr.Annotations = append(declr.Annotations, annons...)
		}

		buff.Reset()
		buffers.Put(buff)
	}

	if ty.Doc != nil && ty.Doc != gen.Doc {
		b.comments[ty.Doc] = struct{}{}
		doc, err := b.handleCommentGroup(ty.Doc)
		if err != nil {
			return nil, err
		}

		declr.Docs = append(declr.Docs, doc)

		buff := buffers.Get().(*bytes.Buffer)
		buff.WriteString(ty.Doc.Text())

		if annons := ParseAnnotationFromReader(buff); len(annons) != 0 {
			declr.Annotations = append(declr.Annotations, annons...)
		}

		buff.Reset()
		buffers.Put(buff)
	}

	if ty.Comment != nil {
		b.comments[ty.Comment] = struct{}{}
		doc, err := b.handleCommentGroup(ty.Comment)
		if err != nil {
			return nil, err
		}

		declr.Docs = append(declr.Docs, doc)

		buff := buffers.Get().(*bytes.Buffer)
		buff.WriteString(ty.Comment.Text())

		if annons := ParseAnnotationFromReader(buff); len(annons) != 0 {
			declr.Annotations = append(declr.Annotations, annons...)
		}

		buff.Reset()
		buffers.Put(buff)
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
				return nil, err
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

	return &declr, nil
}

func (b *ParseScope) handleNamedType(ty *ast.TypeSpec, spec ast.Spec, gen *ast.GenDecl, in chan interface{}) error {
	declr, err := b.transformNamedTypeSpec(ty, spec, gen)
	if err != nil {
		return err
	}

	in <- declr
	return nil
}

func (b *ParseScope) transformNamedTypeSpec(ty *ast.TypeSpec, spec ast.Spec, gen *ast.GenDecl) (*Type, error) {
	var declr Type
	declr.Name = ty.Name.Name
	declr.Methods = map[string]*Function{}
	declr.Location = b.getLocation(ty.Pos(), ty.End())

	if gen.Doc != nil {
		b.comments[gen.Doc] = struct{}{}
		doc, err := b.handleCommentGroup(gen.Doc)
		if err != nil {
			return nil, err
		}

		declr.Doc = doc

		buff := buffers.Get().(*bytes.Buffer)
		buff.WriteString(gen.Doc.Text())

		if annons := ParseAnnotationFromReader(buff); len(annons) != 0 {
			declr.Annotations = append(declr.Annotations, annons...)
		}

		buff.Reset()
		buffers.Put(buff)
	}

	if ty.Doc != nil && gen.Doc != ty.Doc {
		b.comments[ty.Doc] = struct{}{}
		doc, err := b.handleCommentGroup(ty.Doc)
		if err != nil {
			return nil, err
		}

		declr.Docs = append(declr.Docs, doc)

		buff := buffers.Get().(*bytes.Buffer)
		buff.WriteString(ty.Doc.Text())

		if annons := ParseAnnotationFromReader(buff); len(annons) != 0 {
			declr.Annotations = append(declr.Annotations, annons...)
		}

		buff.Reset()
		buffers.Put(buff)
	}

	if ty.Comment != nil {
		b.comments[ty.Comment] = struct{}{}
		doc, err := b.handleCommentGroup(ty.Comment)
		if err != nil {
			return nil, err
		}

		declr.Docs = append(declr.Docs, doc)

		buff := buffers.Get().(*bytes.Buffer)
		buff.WriteString(ty.Comment.Text())

		if annons := ParseAnnotationFromReader(buff); len(annons) != 0 {
			declr.Annotations = append(declr.Annotations, annons...)
		}

		buff.Reset()
		buffers.Put(buff)
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

	return &declr, nil
}

func (b *ParseScope) handleFunctionSpec(fn *ast.FuncDecl, in chan interface{}) error {
	fnm, err := b.transformFunctionSpec(fn)
	if err != nil {
		return err
	}

	in <- fnm
	return nil
}

func (b *ParseScope) transformFunctionSpec(fn *ast.FuncDecl) (*Function, error) {
	var declr Function
	declr.Name = fn.Name.Name
	declr.ScopeName = fn.Name.Name

	if declr.Name == "" {
		declr.Name = String(10)
	}

	if fn.Doc != nil {
		b.comments[fn.Doc] = struct{}{}
		doc, err := b.handleCommentGroup(fn.Doc)
		if err != nil {
			return nil, err
		}

		declr.Doc = doc

		buff := buffers.Get().(*bytes.Buffer)
		buff.WriteString(fn.Doc.Text())

		if annons := ParseAnnotationFromReader(buff); len(annons) != 0 {
			declr.Annotations = append(declr.Annotations, annons...)
		}

		buff.Reset()
		buffers.Put(buff)
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
			return nil, err
		}
	}

	if fn.Type.Results != nil {
		declr.Returns, err = b.handleParameterList(fn.Name.Name, fn.Type.Results)
		if err != nil {
			return nil, err
		}
	}

	if fn.Recv == nil {
		declr.Path = strings.Join([]string{obj.Pkg().Path(), fn.Name.Name}, ".")

		b.scopes[declr.Path] = FunctionScope{
			Name:  declr.Name,
			Path:  declr.Path,
			Scope: map[string]Expr{},
		}

		return &declr, nil
	}

	if len(fn.Recv.List) == 0 {
		declr.Path = strings.Join([]string{obj.Pkg().Path(), fn.Name.Name}, ".")

		b.scopes[declr.Path] = FunctionScope{
			Name:  declr.Name,
			Path:  declr.Path,
			Scope: map[string]Expr{},
		}

		return &declr, nil
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
			return nil, errors.New("ast.StarExpr does not use ast.Ident as X: %#v", head.X)
		}

		declr.IsPointerMethod = true
		declr.Path = strings.Join([]string{obj.Pkg().Path(), headr.Name, fn.Name.Name}, ".")
		declr.ReceiverAddr = strings.Join([]string{obj.Pkg().Path(), headr.Name, fn.Name.Name, instanceName.Name}, ".")
	case *ast.Ident:
		declr.Path = strings.Join([]string{obj.Pkg().Path(), head.Name, fn.Name.Name}, ".")
		declr.ReceiverAddr = strings.Join([]string{obj.Pkg().Path(), head.Name, fn.Name.Name, instanceName.Name}, ".")
	}

	fnAddr := &declr
	b.scopes[declr.Path] = FunctionScope{
		Name:  declr.Name,
		Path:  declr.Path,
		Scope: map[string]Expr{},
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

		// Collected all scoped objects and remove key from map.
		scoped := b.scopes[fnAddr.Path].Scope
		fnAddr.Scope = scoped
		delete(b.scopes, fnAddr.Path)

		return nil
	}

	return &declr, nil
}

func (b *ParseScope) handleFunctionLit(fn *ast.FuncLit, others map[string]*Package) (*Function, error) {
	declr, err := b.handleFunctionType(String(10), fn.Type)
	if err != nil {
		return nil, err
	}

	// read location information(line, column, source text etc) for giving type.
	declr.Location = b.getLocation(fn.Pos(), fn.End())

	if fn.Body == nil {
		return declr, nil
	}

	declr.resolver = func(others map[string]*Package) error {
		body, err := b.handleBlockStmt(declr, fn.Body, others)
		if err != nil {
			return err
		}
		declr.Body = body

		// Collected all scoped objects and remove key from map.
		scoped := b.scopes[declr.Path].Scope
		declr.Scope = scoped
		delete(b.scopes, declr.Path)
		return nil
	}

	b.From.addProc(declr.resolver)
	return declr, nil
}

func (b *ParseScope) handleFunctionType(name string, fn *ast.FuncType) (*Function, error) {
	var declr Function
	declr.Name = String(10)
	declr.ScopeName = String(10)
	declr.Path = strings.Join([]string{b.From.Name, name, declr.Name}, ".")

	// read location information(line, column, source text etc) for giving type.
	declr.Location = b.getLocation(fn.Pos(), fn.End())

	b.scopes[declr.Path] = FunctionScope{
		Name:  declr.Name,
		Path:  declr.Path,
		Scope: map[string]Expr{},
	}

	var err error
	if fn.Params != nil {
		declr.Arguments, err = b.handleParameterList(name, fn.Params)
		if err != nil {
			return nil, err
		}
	}

	if fn.Results != nil {
		declr.Returns, err = b.handleParameterList(name, fn.Results)
		if err != nil {
			return nil, err
		}
	}

	return &declr, nil
}

func (b *ParseScope) handleFunctionFieldList(ownerName string, set *ast.FieldList) ([]*Function, error) {
	var params []*Function
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

func (b *ParseScope) handleFieldList(ownerName string, set *ast.FieldList) ([]*Field, error) {
	var params []*Field
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

func (b *ParseScope) handleFieldMap(ownerName string, set *ast.FieldList) (map[string]*Field, error) {
	params := map[string]*Field{}

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

func (b *ParseScope) handleParameterList(fnName string, set *ast.FieldList) ([]*Parameter, error) {
	var params []*Parameter
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
		return nil
	case Interface:
		host.Composes[identity.ID()] = &embed
		return nil
	}

	return errors.New("expected type should be an Interface or *Interface: %#v", identity)
}

func (b *ParseScope) handleFunctionField(ownerName string, f *ast.Field, t ast.Expr) (*Function, error) {
	var p Function
	p.Name = String(10)
	p.Path = strings.Join([]string{b.From.Name, p.Name}, ".")

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

		buff := buffers.Get().(*bytes.Buffer)
		buff.WriteString(f.Doc.Text())

		if annons := ParseAnnotationFromReader(buff); len(annons) != 0 {
			p.Annotations = append(p.Annotations, annons...)
		}

		buff.Reset()
		buffers.Put(buff)
	}

	if f.Comment != nil {
		b.comments[f.Comment] = struct{}{}
		doc, cerr := b.handleCommentGroup(f.Comment)
		if cerr != nil {
			return nil, cerr
		}
		p.Docs = append(p.Docs, doc)

		buff := buffers.Get().(*bytes.Buffer)
		buff.WriteString(f.Comment.Text())

		if annons := ParseAnnotationFromReader(buff); len(annons) != 0 {
			p.Annotations = append(p.Annotations, annons...)
		}

		buff.Reset()
		buffers.Put(buff)
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
			return nil, cerr
		}
		p.Docs = append(p.Docs, doc)

		buff := buffers.Get().(*bytes.Buffer)
		buff.WriteString(f.Doc.Text())

		if annons := ParseAnnotationFromReader(buff); len(annons) != 0 {
			p.Annotations = append(p.Annotations, annons...)
		}

		buff.Reset()
		buffers.Put(buff)
	}

	if f.Comment != nil {
		b.comments[f.Comment] = struct{}{}
		doc, cerr := b.handleCommentGroup(f.Comment)
		if cerr != nil {
			return nil, cerr
		}
		p.Docs = append(p.Docs, doc)

		buff := buffers.Get().(*bytes.Buffer)
		buff.WriteString(f.Comment.Text())

		if annons := ParseAnnotationFromReader(buff); len(annons) != 0 {
			p.Annotations = append(p.Annotations, annons...)
		}

		buff.Reset()
		buffers.Put(buff)
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

	return p, nil
}

func (b *ParseScope) handleField(ownerName string, f *ast.Field, t ast.Expr) (*Field, error) {
	var p Field

	if f.Tag != nil {
		p.Tags = getTags(f.Tag.Value)
	}

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

	return pAddr, nil
}

func (b *ParseScope) handleFieldWithName(ownerName string, f *ast.Field, nm *ast.Ident, t ast.Expr) (*Field, error) {
	var p Field
	p.Name = nm.Name

	if f.Tag != nil {
		p.Tags = getTags(f.Tag.Value)
	}

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

	return pAddr, nil
}

func (b *ParseScope) handleParameter(fnName string, f *ast.Field, t ast.Expr) (*Parameter, error) {
	var p Parameter

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
		return pAddr, nil
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

	return pAddr, nil
}

func (b *ParseScope) handleParameterWithName(fnName string, f *ast.Field, nm *ast.Ident, t ast.Expr) (*Parameter, error) {
	var p Parameter
	p.Name = nm.Name

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
		return pAddr, nil
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

	return pAddr, nil
}

func (b *ParseScope) handleBuildCommentary() {
	for _, cdoc := range b.File.Comments {
		if cdoc.Text() == "//" {
			continue
		}

		text := cdoc.Text()
		text = strings.TrimPrefix(text, "//")
		text = strings.TrimSpace(text)

		if !strings.HasPrefix(text, "+build") {
			continue
		}

		text = strings.TrimPrefix(text, "+build")
		text = strings.TrimSpace(text)
		archs := strings.Split(text, " ")
		for _, arch := range archs {
			if strings.Contains(arch, ",") {
				for _, ppl := range strings.Split(arch, ",") {
					if strings.Contains(ppl, "cgo") && !strings.HasPrefix(ppl, "!") {
						b.Package.Cgo = true
						b.Package.Archs["cgo"] = true
						continue
					}

					if strings.HasPrefix(ppl, "!") {
						continue
					}

					b.Package.Archs[ppl] = true
				}
				continue
			}

			if strings.Contains(arch, "cgo") && !strings.HasPrefix(arch, "!") {
				b.Package.Cgo = true
				b.Package.Archs["cgo"] = true
				continue
			}

			if strings.HasPrefix(arch, "!") {
				continue
			}

			b.Package.Archs[arch] = true
		}
	}
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

		buff := buffers.Get().(*bytes.Buffer)
		buff.WriteString(doc.Text)

		if annons := ParseAnnotationFromReader(buff); len(annons) != 0 {
			b.From.Annotations = append(b.From.Annotations, annons...)
		}

		buff.Reset()
		buffers.Put(buff)

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
	platform, arch := getPlatformArchitecture(filepath.Base(begin.Filename))

	archs := make(map[string]bool, len(b.Package.Archs)+1)
	for k := range b.Package.Archs {
		archs[k] = true
	}

	if arch != "" {
		archs[arch] = true
	}

	platforms := make(map[string]bool, len(b.Package.Platforms)+1)
	for k := range b.Package.Platforms {
		platforms[k] = true
	}

	if platform != "" {
		platforms[platform] = true
	}

	var loc Location
	loc.Archs = archs
	loc.Cgo = b.Package.Cgo
	loc.Platforms = platforms
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
	return Import{}, errors.New("import path with alias %q not found", aliasName)
}

func (b *ParseScope) getTypeFromTypeSpecExpr(e ast.Expr, t *ast.TypeSpec, others map[string]*Package) (Expr, error) {
	base, err := b.transformTypeFor(e, others)
	if err != nil {
		return nil, err
	}
	return base, nil
}

func (b *ParseScope) getTypeFromFieldExpr(e ast.Expr, f *ast.Field, others map[string]*Package) (Expr, error) {
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
		stmt, err := b.transformFunctionExpr(block, fn, dn, others)
		if err != nil {
			return nil, err
		}
		gp.Children = append(gp.Children, stmt)
	}

	return &gp, nil
}

func (b *ParseScope) transformFunctionExpr(e interface{}, bn *ast.BlockStmt, fn *Function, others map[string]*Package) (Expr, error) {
	switch tm := e.(type) {
	case *ast.CommClause:
		return b.transformCommClauseStmt(fn, bn, tm, others)
	case *ast.CaseClause:
		return b.transformCaseClauseStmt(fn, bn, tm, others)
	case *ast.SwitchStmt:
		return b.transformSwitchStatement(fn, bn, tm, others)
	case *ast.AssignStmt:
		return b.transformAssignStmt(fn, bn, tm, others)
	case *ast.BlockStmt:
		return b.transformBlockStmt(fn, bn, tm, others)
	case *ast.EmptyStmt:
		return b.transformEmptyStmt(fn, bn, tm, others)
	case *ast.BranchStmt:
		return b.transformBranchStmt(fn, bn, tm, others)
	case *ast.DeferStmt:
		return b.transformDeferStmt(fn, bn, tm, others)
	case *ast.DeclStmt:
		return b.transformDeclrStmt(fn, bn, tm, others)
	case *ast.BadStmt:
		return b.transformBadStmt(fn, bn, tm, others)
	case *ast.GoStmt:
		return b.transformGoStmt(fn, bn, tm, others)
	case *ast.RangeStmt:
		return b.transformRangeStmt(fn, bn, tm, others)
	case *ast.TypeSwitchStmt:
		return b.transformTypeSwitchStmt(fn, bn, tm, others)
	case *ast.SendStmt:
		return b.transformSendStmt(fn, bn, tm, others)
	case *ast.SelectStmt:
		return b.transformSelectStmt(fn, bn, tm, others)
	case *ast.ReturnStmt:
		return b.transformReturnStmt(fn, bn, tm, others)
	case *ast.LabeledStmt:
		return b.transformLabeledStmt(fn, bn, tm, others)
	case *ast.IncDecStmt:
		return b.transformIncDecStmt(fn, bn, tm, others)
	case *ast.IfStmt:
		return b.transformIfStmt(fn, bn, tm, others)
	case *ast.ForStmt:
		return b.transformForStmt(fn, bn, tm, others)
	case *ast.ExprStmt:
		return b.transformExprStmt(fn, bn, tm, others)
	case *ast.BasicLit:
		return b.transformBasicLit(tm, others)
	case *ast.KeyValueExpr:
		return b.transformFunctionKeyValueExpr(tm, bn, fn, others)
	case *ast.TypeAssertExpr:
		return b.transformFunctionTypeAssertExpr(tm, bn, fn, others)
	case *ast.BinaryExpr:
		return b.transformFunctionBinaryExpr(tm, bn, fn, others)
	case *ast.IndexExpr:
		return b.transformFunctionIndexExpr(tm, bn, fn, others)
	case *ast.UnaryExpr:
		return b.transformFunctionUnaryExpr(tm, bn, fn, others)
	case *ast.ParenExpr:
		return b.transformFunctionParentExpr(tm, bn, fn, others)
	case *ast.CompositeLit:
		return b.transformFunctionCompositeLitValue(tm, bn, fn, others)
	case *ast.CallExpr:
		return b.transformFunctionCallExpr(tm, bn, fn, others)
	case *ast.StarExpr:
		return b.transformFunctionStarExpr(tm, bn, fn, others)
	case *ast.Ident:
		return b.transformFunctionIdentExpr(tm, bn, fn, others)
	case *ast.SliceExpr:
		return b.transformFunctionSliceExpr(tm, bn, fn, others)
	case *ast.SelectorExpr:
		return b.transformFunctionSelectorExpr(tm, bn, fn, others)
	case *ast.MapType:
		return b.transformFunctionMapType(tm, bn, fn, others)
	case *ast.ChanType:
		return b.transformFunctionChanType(tm, bn, fn, others)
	case *ast.ArrayType:
		return b.transformFunctionArrayType(tm, bn, fn, others)
	case *ast.FuncType:
		return b.transformFuncType(tm, others)
	case *ast.FuncLit:
		return b.transformFunctionFuncList(tm, bn, fn, others)
	case *ast.InterfaceType:
		return b.transformInterfaceType(tm, others)
	case *ast.StructType:
		return b.transformStructType(tm, others)
	}

	return nil, errors.New("unable to handle function body type: %#v", e)
}

func (b *ParseScope) transformEmptyStmt(dn *Function, fn *ast.BlockStmt, sw *ast.EmptyStmt, others map[string]*Package) (Expr, error) {
	var stmt EmptyExpr
	stmt.Implicit = sw.Implicit
	stmt.Location = b.getLocation(sw.Pos(), sw.End())
	return &stmt, nil
}

func (b *ParseScope) transformExprStmt(dn *Function, fn *ast.BlockStmt, sw *ast.ExprStmt, others map[string]*Package) (*StmtExpr, error) {
	var stmt StmtExpr
	stmt.Location = b.getLocation(sw.Pos(), sw.End())

	xd, err := b.transformFunctionExpr(sw.X, fn, dn, others)
	if err != nil {
		return nil, err
	}

	stmt.X = xd
	return &stmt, nil
}

func (b *ParseScope) transformForStmt(dn *Function, fn *ast.BlockStmt, sw *ast.ForStmt, others map[string]*Package) (*ForExpr, error) {
	var stmt ForExpr
	stmt.Location = b.getLocation(sw.Pos(), sw.End())

	if sw.Cond != nil {
		cond, err := b.transformFunctionExpr(sw.Cond, fn, dn, others)
		if err != nil {
			return nil, err
		}

		stmt.Cond = cond
	}

	if sw.Post != nil {
		postd, err := b.transformFunctionExpr(sw.Post, fn, dn, others)
		if err != nil {
			return nil, err
		}

		stmt.Post = postd
	}

	if sw.Init != nil {
		initd, err := b.transformFunctionExpr(sw.Init, fn, dn, others)
		if err != nil {
			return nil, err
		}

		stmt.Init = initd
	}

	body, err := b.transformFunctionExpr(sw.Body, fn, dn, others)
	if err != nil {
		return nil, err
	}

	gbody, ok := body.(*GroupStmt)
	if !ok {
		return nil, errors.New("body of RangeStmt must be type *GroupStmt")
	}

	stmt.Body = gbody
	return &stmt, nil
}

func (b *ParseScope) transformIfStmt(dn *Function, fn *ast.BlockStmt, sw *ast.IfStmt, others map[string]*Package) (*IfExpr, error) {
	var stmt IfExpr
	stmt.Location = b.getLocation(sw.Pos(), sw.End())

	cond, err := b.transformFunctionExpr(sw.Cond, fn, dn, others)
	if err != nil {
		return nil, err
	}

	stmt.Cond = cond

	if sw.Init != nil {
		inited, cerr := b.transformFunctionExpr(sw.Init, fn, dn, others)
		if cerr != nil {
			return nil, cerr
		}

		stmt.Init = inited
	}

	body, err := b.transformFunctionExpr(sw.Body, fn, dn, others)
	if err != nil {
		return nil, err
	}

	gbody, ok := body.(*GroupStmt)
	if !ok {
		return nil, errors.New("body of RangeStmt must be type *GroupStmt")
	}

	stmt.Body = gbody

	if sw.Else != nil {
		elsed, err := b.transformFunctionExpr(sw.Else, fn, dn, others)
		if err != nil {
			return nil, err
		}

		stmt.Else = elsed
	}

	return &stmt, nil
}

func (b *ParseScope) transformIncDecStmt(dn *Function, fn *ast.BlockStmt, sw *ast.IncDecStmt, others map[string]*Package) (*IncDecExpr, error) {
	var stmt IncDecExpr
	stmt.Location = b.getLocation(sw.Pos(), sw.End())

	if sw.Tok.String() == "--" || sw.Tok.String() == "-" {
		stmt.Dec = true
	} else {
		stmt.Inc = true
	}

	val, err := b.transformFunctionExpr(sw.X, fn, dn, others)
	if err != nil {
		return nil, err
	}

	stmt.Target = val
	return &stmt, nil
}

func (b *ParseScope) transformLabeledStmt(dn *Function, fn *ast.BlockStmt, sw *ast.LabeledStmt, others map[string]*Package) (*LabeledExpr, error) {
	var stmt LabeledExpr
	stmt.Label = sw.Label.Name
	stmt.Location = b.getLocation(sw.Pos(), sw.End())

	stm, err := b.transformFunctionExpr(sw.Stmt, fn, dn, others)
	if err != nil {
		return nil, err
	}

	stmt.Stmt = stm

	return &stmt, nil
}

func (b *ParseScope) transformReturnStmt(dn *Function, fn *ast.BlockStmt, sw *ast.ReturnStmt, others map[string]*Package) (*ReturnsExpr, error) {
	var stmt ReturnsExpr
	stmt.Results = make([]Expr, 0, len(sw.Results))
	stmt.Location = b.getLocation(sw.Pos(), sw.End())

	for _, elem := range sw.Results {
		celem, err := b.transformFunctionExpr(elem, fn, dn, others)
		if err != nil {
			return nil, err
		}

		stmt.Results = append(stmt.Results, celem)
	}

	return &stmt, nil
}

func (b *ParseScope) transformSelectStmt(dn *Function, fn *ast.BlockStmt, sw *ast.SelectStmt, others map[string]*Package) (*SelectExpr, error) {
	var stmt SelectExpr
	stmt.Location = b.getLocation(sw.Pos(), sw.End())

	body, err := b.transformFunctionExpr(sw.Body, fn, dn, others)
	if err != nil {
		return nil, err
	}

	gbody, ok := body.(*GroupStmt)
	if !ok {
		return nil, errors.New("body of RangeStmt must be type *GroupStmt")
	}

	stmt.Body = gbody

	return &stmt, nil
}

func (b *ParseScope) transformSendStmt(dn *Function, fn *ast.BlockStmt, sw *ast.SendStmt, others map[string]*Package) (*SendExpr, error) {
	var stmt SendExpr
	stmt.Location = b.getLocation(sw.Pos(), sw.End())

	ch, err := b.transformFunctionExpr(sw.Chan, fn, dn, others)
	if err != nil {
		return nil, err
	}

	stmt.Chan = ch

	val, err := b.transformFunctionExpr(sw.Value, fn, dn, others)
	if err != nil {
		return nil, err
	}

	stmt.Value = val

	return &stmt, nil
}

func (b *ParseScope) transformTypeSwitchStmt(dn *Function, fn *ast.BlockStmt, sw *ast.TypeSwitchStmt, others map[string]*Package) (*TypeSwitchExpr, error) {
	var stmt TypeSwitchExpr
	stmt.Location = b.getLocation(sw.Pos(), sw.End())

	if sw.Assign != nil {
		assigned, err := b.transformFunctionExpr(sw.Assign, fn, dn, others)
		if err != nil {
			return nil, err
		}

		stmt.Assign = assigned
	}

	if sw.Init != nil {
		initd, err := b.transformFunctionExpr(sw.Init, fn, dn, others)
		if err != nil {
			return nil, err
		}

		stmt.Init = initd
	}

	body, err := b.transformFunctionExpr(sw.Body, fn, dn, others)
	if err != nil {
		return nil, err
	}

	gbody, ok := body.(*GroupStmt)
	if !ok {
		return nil, errors.New("body of RangeStmt must be type *GroupStmt")
	}

	stmt.Body = gbody

	return &stmt, nil
}

func (b *ParseScope) transformRangeStmt(dn *Function, fn *ast.BlockStmt, sw *ast.RangeStmt, others map[string]*Package) (*RangeExpr, error) {
	var stmt RangeExpr
	stmt.Location = b.getLocation(sw.Pos(), sw.End())

	if sw.Body != nil {
		body, err := b.transformFunctionExpr(sw.Body, fn, dn, others)
		if err != nil {
			return nil, err
		}

		gbody, ok := body.(*GroupStmt)
		if !ok {
			return nil, errors.New("body of RangeStmt must be type *GroupStmt")
		}

		stmt.Body = gbody
	}

	if sw.X != nil {
		x, err := b.transformFunctionExpr(sw.X, fn, dn, others)
		if err != nil {
			return nil, err
		}

		stmt.X = x
	}

	if sw.Key != nil {
		key, err := b.transformFunctionExpr(sw.Key, fn, dn, others)
		if err != nil {
			return nil, err
		}

		stmt.Key = key
	}

	if sw.Value != nil {
		val, err := b.transformFunctionExpr(sw.Value, fn, dn, others)
		if err != nil {
			return nil, err
		}

		stmt.Value = val
	}

	return &stmt, nil
}

func (b *ParseScope) transformDeclrStmt(dn *Function, fn *ast.BlockStmt, sw *ast.DeclStmt, others map[string]*Package) (*DeclrExpr, error) {
	var stmt DeclrExpr
	stmt.Location = b.getLocation(sw.Pos(), sw.End())

	if sw.Decl == nil {
		return &stmt, nil
	}

	declr, ok := sw.Decl.(*ast.GenDecl)
	if !ok {
		return nil, errors.New("ast.DeclStmt.Declr must be a *ast.GenDeclr")
	}

	for _, spec := range declr.Specs {
		exprs, err := b.transformDeclrSpecStmt(spec, declr)
		if err != nil {
			return nil, err
		}

		stmt.Declr = append(stmt.Declr, exprs...)
	}

	return &stmt, nil
}

func (b *ParseScope) transformDeclrVariables(val *ast.ValueSpec, spec ast.Spec, gen *ast.GenDecl) ([]Expr, error) {
	exprs := make([]Expr, 0, len(val.Names))
	for index, named := range val.Names {
		vard, err := b.transformVariable(index, named, val, spec, gen)
		if err != nil {
			return nil, err
		}

		b.From.addProc(vard.Resolve)
		exprs = append(exprs, vard)
	}
	return exprs, nil
}

func (b *ParseScope) transformDeclrSpecStmt(spec ast.Spec, gen *ast.GenDecl) ([]Expr, error) {
	switch obj := spec.(type) {
	case *ast.ValueSpec:
		return b.transformDeclrVariables(obj, spec, gen)
	case *ast.TypeSpec:
		switch ty := obj.Type.(type) {
		case *ast.InterfaceType:
			id, err := b.transformInterfaceSpec(ty, obj, spec, gen)
			if err != nil {
				return nil, err
			}
			b.From.addProc(id.Resolve)
			return []Expr{id}, nil
		case *ast.StructType:
			id, err := b.transformStructDeclr(ty, obj, spec, gen)
			if err != nil {
				return nil, err
			}
			b.From.addProc(id.Resolve)
			return []Expr{id}, nil
		default:
			id, err := b.transformNamedTypeSpec(obj, spec, gen)
			if err != nil {
				return nil, err
			}

			b.From.addProc(id.Resolve)
			return []Expr{id}, nil
		}
	}
	return nil, errors.New("unable to handle ast.Spec %T", spec)
}

func (b *ParseScope) transformGoStmt(dn *Function, fn *ast.BlockStmt, sw *ast.GoStmt, others map[string]*Package) (*GoExpr, error) {
	var stmt GoExpr
	stmt.Location = b.getLocation(sw.Pos(), sw.End())

	elem, err := b.transformFunctionExpr(sw.Call, fn, dn, others)
	if err != nil {
		return nil, err
	}

	called, ok := elem.(*CallExpr)
	if !ok {
		return nil, errors.New("type must be a *CallExpr: %#v", elem)
	}

	stmt.Fn = called
	return &stmt, nil
}

func (b *ParseScope) transformBadStmt(dn *Function, fn *ast.BlockStmt, sw *ast.BadStmt, others map[string]*Package) (*BadExpr, error) {
	var stmt BadExpr
	stmt.Location = b.getLocation(sw.Pos(), sw.End())
	return &stmt, nil
}

func (b *ParseScope) transformDeferStmt(dn *Function, fn *ast.BlockStmt, sw *ast.DeferStmt, others map[string]*Package) (*DeferExpr, error) {
	var stmt DeferExpr
	stmt.Location = b.getLocation(sw.Pos(), sw.End())

	elem, err := b.transformFunctionExpr(sw.Call, fn, dn, others)
	if err != nil {
		return nil, err
	}

	called, ok := elem.(*CallExpr)
	if !ok {
		return nil, errors.New("type must be a *CallExpr: %#v", elem)
	}

	stmt.Fn = called
	return &stmt, nil
}

func (b *ParseScope) transformBranchStmt(dn *Function, fn *ast.BlockStmt, sw *ast.BranchStmt, others map[string]*Package) (*BranchExpr, error) {
	var stmt BranchExpr
	stmt.Location = b.getLocation(sw.Pos(), sw.End())

	if sw.Label != nil {
		elem, err := b.transformFunctionExpr(sw.Label, fn, dn, others)
		if err != nil {
			return nil, err
		}

		stmt.Label = elem
	}

	return &stmt, nil
}

func (b *ParseScope) transformAssignStmt(dn *Function, fn *ast.BlockStmt, sw *ast.AssignStmt, others map[string]*Package) (*AssignExpr, error) {
	// Get the function scope map.
	scope := b.scopes[dn.Path]

	var stmt AssignExpr
	stmt.Pairs = make([]VariableValuePair, 0, len(sw.Lhs))
	stmt.Location = b.getLocation(sw.Pos(), sw.End())

	assignedLeft := len(sw.Lhs)
	assignedRight := len(sw.Rhs)

	// if we have a case where the right is 1 and the left is more than one then the values for left is the values
	// received from right.
	if assignedRight == 1 && assignedLeft > 1 {
		varValue, err := b.transformFunctionExpr(sw.Rhs[0], fn, dn, others)
		if err != nil {
			return nil, err
		}

		for _, key := range sw.Lhs {
			varKey, err := b.transformFunctionExpr(key, fn, dn, others)
			if err != nil {
				return nil, err
			}

			var pair VariableValuePair
			pair.Key = varKey
			pair.Value = varValue
			pair.Location = b.getLocation(key.Pos(), key.End())
			stmt.Pairs = append(stmt.Pairs, pair)

			scopedPath := strings.Join([]string{dn.Name, varKey.ID()}, ".")
			scope.Scope[scopedPath] = varValue
		}

		return &stmt, nil
	}

	if assignedLeft != assignedRight {
		return nil, errors.New("Numbers of assigned must be equal to number of declared values: Values: %d Assigned: %d", assignedLeft, assignedRight)
	}

	// Here the total values of left and right must be the same.
	for index, key := range sw.Lhs {
		varKey, err := b.transformFunctionExpr(key, fn, dn, others)
		if err != nil {
			return nil, err
		}

		value := sw.Rhs[index]
		varValue, err := b.transformFunctionExpr(value, fn, dn, others)
		if err != nil {
			return nil, err
		}

		var pair VariableValuePair
		pair.Key = varKey
		pair.Value = varValue
		pair.Location = b.getLocation(key.Pos(), key.End())
		stmt.Pairs = append(stmt.Pairs, pair)

		scopedPath := strings.Join([]string{dn.Name, varKey.ID()}, ".")
		scope.Scope[scopedPath] = varValue
	}

	return &stmt, nil
}

func (b *ParseScope) transformBlockStmt(dn *Function, pa *ast.BlockStmt, sw *ast.BlockStmt, others map[string]*Package) (*GroupStmt, error) {
	var gp GroupStmt
	gp.EndSymbol = "}"
	gp.BeginSymbol = "{"
	gp.Type = StatementBlock
	gp.Children = make([]Expr, 0, len(sw.List))

	// read location information(line, column, source text etc) for giving type.
	gp.Location = b.getLocation(sw.Pos(), sw.End())

	for _, block := range sw.List {
		stmt, err := b.transformFunctionExpr(block, sw, dn, others)
		if err != nil {
			return nil, err
		}
		gp.Children = append(gp.Children, stmt)
	}

	return &gp, nil
}

func (b *ParseScope) transformCommClauseStmt(dn *Function, fn *ast.BlockStmt, sw *ast.CommClause, others map[string]*Package) (*SelectCaseExpr, error) {
	var stmt SelectCaseExpr
	stmt.Body = make([]Expr, 0, len(sw.Body))
	stmt.Location = b.getLocation(sw.Pos(), sw.End())

	if sw.Comm != nil {
		elem, err := b.transformFunctionExpr(sw.Comm, fn, dn, others)
		if err != nil {
			return nil, err
		}

		stmt.Comm = elem
	}

	for _, item := range sw.Body {
		elem, err := b.transformFunctionExpr(item, fn, dn, others)
		if err != nil {
			return nil, err
		}

		stmt.Body = append(stmt.Body, elem)
	}

	return &stmt, nil
}

func (b *ParseScope) transformCaseClauseStmt(dn *Function, fn *ast.BlockStmt, sw *ast.CaseClause, others map[string]*Package) (*SwitchCaseExpr, error) {
	var stmt SwitchCaseExpr
	stmt.Body = make([]Expr, 0, len(sw.Body))
	stmt.Conditions = make([]Expr, 0, len(sw.List))
	stmt.Location = b.getLocation(sw.Pos(), sw.End())

	for _, item := range sw.List {
		elem, err := b.transformFunctionExpr(item, fn, dn, others)
		if err != nil {
			return nil, err
		}

		stmt.Conditions = append(stmt.Conditions, elem)
	}

	for _, item := range sw.Body {
		elem, err := b.transformFunctionExpr(item, fn, dn, others)
		if err != nil {
			return nil, err
		}

		stmt.Body = append(stmt.Body, elem)
	}

	return &stmt, nil
}

func (b *ParseScope) transformSwitchStatement(dn *Function, fn *ast.BlockStmt, sw *ast.SwitchStmt, others map[string]*Package) (*SwitchExpr, error) {
	var stmt SwitchExpr
	stmt.Location = b.getLocation(sw.Pos(), sw.End())

	if sw.Init != nil {
		initd, err := b.transformFunctionExpr(sw.Init, fn, dn, others)
		if err != nil {
			return nil, err
		}

		stmt.Init = initd
	}

	if sw.Body != nil {
		body, err := b.transformFunctionExpr(sw.Body, fn, dn, others)
		if err != nil {
			return nil, err
		}

		gbody, ok := body.(*GroupStmt)
		if !ok {
			return nil, errors.New("body of RangeStmt must be type *GroupStmt")
		}

		stmt.Body = gbody
	}

	if sw.Tag != nil {
		tags, err := b.transformFunctionExpr(sw.Tag, fn, dn, others)
		if err != nil {
			return nil, err
		}

		stmt.Tag = tags
	}

	return &stmt, nil
}

//**********************************************************************************
// Function Body Field Relation Functions
//**********************************************************************************

func (b *ParseScope) transformFunctionFuncList(src *ast.FuncLit, bn *ast.BlockStmt, fn *Function, others map[string]*Package) (*Function, error) {
	fm, err := b.handleFunctionLit(src, others)
	if err != nil {
		return nil, err
	}

	scope := b.scopes[fn.Path]
	b.parentScopes[fm.Path] = scope
	return fm, nil
}

func (b *ParseScope) transformFunctionIndexExpr(src *ast.IndexExpr, bn *ast.BlockStmt, fn *Function, others map[string]*Package) (*IndexExpr, error) {
	var val IndexExpr
	val.Location = b.getLocation(src.Pos(), src.End())

	elem, err := b.transformFunctionExpr(src.X, bn, fn, others)
	if err != nil {
		return nil, err
	}
	val.Elem = elem

	indexed, err := b.transformFunctionExpr(src.Index, bn, fn, others)
	if err != nil {
		return nil, err
	}

	val.Index = indexed

	return &val, nil
}

func (b *ParseScope) transformFunctionTypeAssertExpr(src *ast.TypeAssertExpr, bn *ast.BlockStmt, fn *Function, others map[string]*Package) (*TypeAssert, error) {
	var ts TypeAssert

	// read location information(line, column, source text etc) for giving type.
	ts.Location = b.getLocation(src.Pos(), src.End())

	xt, err := b.transformFunctionExpr(src.X, bn, fn, others)
	if err != nil {
		return nil, err
	}

	ts.X = xt

	if src.Type != nil {
		tt, err := b.transformFunctionExpr(src.Type, bn, fn, others)
		if err != nil {
			return nil, err
		}

		ts.Type = tt
	}

	return &ts, nil
}

func (b *ParseScope) transformFunctionKeyValueExpr(src *ast.KeyValueExpr, bn *ast.BlockStmt, fn *Function, others map[string]*Package) (*KeyValuePair, error) {
	var pair KeyValuePair

	// read location information(line, column, source text etc) for giving type.
	pair.Location = b.getLocation(src.Pos(), src.End())

	nkey, err := b.transformFunctionExpr(src.Key, bn, fn, others)
	if err != nil {
		return nil, err
	}

	pair.Key = nkey

	value, err := b.transformFunctionExpr(src.Value, bn, fn, others)
	if err != nil {
		return nil, err
	}

	pair.Value = value

	return &pair, nil
}

func (b *ParseScope) transformFunctionUnaryExpr(src *ast.UnaryExpr, bn *ast.BlockStmt, fn *Function, others map[string]*Package) (Expr, error) {
	base, err := b.transformFunctionExpr(src.X, bn, fn, others)
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

func (b *ParseScope) transformFunctionParentExpr(src *ast.ParenExpr, bn *ast.BlockStmt, fn *Function, others map[string]*Package) (*ParenExpr, error) {
	var bin ParenExpr
	elem, err := b.transformFunctionExpr(src.X, bn, fn, others)
	if err != nil {
		return nil, err
	}
	bin.Elem = elem
	return &bin, nil
}

func (b *ParseScope) transformFunctionCallExpr(src *ast.CallExpr, bn *ast.BlockStmt, fn *Function, others map[string]*Package) (*CallExpr, error) {
	var val CallExpr
	val.Location = b.getLocation(src.Pos(), src.End())

	fnx, err := b.transformFunctionExpr(src.Fun, bn, fn, others)
	if err != nil {
		return nil, err
	}

	val.Func = fnx

	for _, item := range src.Args {
		used, err := b.transformFunctionExpr(item, bn, fn, others)
		if err != nil {
			return nil, err
		}
		val.Arguments = append(val.Arguments, used)
	}

	return &val, nil
}

func (b *ParseScope) transformFunctionBinaryExpr(src *ast.BinaryExpr, bn *ast.BlockStmt, fn *Function, others map[string]*Package) (*BinaryExpr, error) {
	var bin BinaryExpr
	bin.Op = src.Op.String()

	leftHand, err := b.transformFunctionExpr(src.X, bn, fn, others)
	if err != nil {
		return nil, err
	}

	rightHand, err := b.transformFunctionExpr(src.Y, bn, fn, others)
	if err != nil {
		return nil, err
	}

	bin.Left = leftHand
	bin.Right = rightHand
	return &bin, nil
}

func (b *ParseScope) transformFunctionStarExpr(e *ast.StarExpr, bn *ast.BlockStmt, fn *Function, others map[string]*Package) (*Pointer, error) {
	var p Pointer
	p.Location = b.getLocation(e.Pos(), e.End())
	elem, err := b.transformFunctionExpr(e.X, bn, fn, others)
	if err != nil {
		return nil, err
	}
	p.Elem = elem
	return &p, nil
}

func (b *ParseScope) transformFunctionIdentExpr(base *ast.Ident, bn *ast.BlockStmt, fn *Function, others map[string]*Package) (Expr, error) {
	scope := b.scopes[fn.Path]

	// if we already have variable name already scoped, then work with that
	if scopedVar, ok := scope.Scope[base.Name]; ok {
		return scopedVar, nil
	}

	// Case 1:
	//
	// If the selectors X is the same with the function receiver then we are
	// probably dealing with a call to the function's owners field.
	if base.Name == fn.ReceiverName {
		// Add giving function owner into scope for giving variable name.
		scope.Scope[base.Name] = fn.Owner
		return fn.Owner, nil
	}

	// Case 2:
	//
	// If its not then we are probably dealing with a previously the functions
	// argument or returned types.
	var found bool
	var target *Parameter
	for _, arg := range fn.Arguments {
		if arg.Name == base.Name {
			target = arg
			found = true
			break
		}
	}

	// SubCase:
	//
	// We will have two cases of checks for not found:
	// 1. The identity is not part of the arguments so must be a named returned type
	// 2. Its neither the argument or returned type hence must be a third case, possible
	// a declared or assigned variable.

	// SubCase 1:
	//
	// Its not an argument, then its possible a named returned type.
	if !found {
		for _, arg := range fn.Returns {
			if arg.Name == base.Name {
				target = arg
				found = true
				break
			}
		}
	}

	// SubCase 2:
	//
	// Its not a named return type, so must be a declared variable.
	// We found a target either in the argument or named returned type.
	// Add base into scope list.
	if found {
		scope.Scope[base.Name] = target
		return target, nil
	}

	// Case 3:
	//
	// If its neither the function owners instance name and arguments, then maybe
	// it's a declared type within function body, so we should look into the function
	// scope because it must at least have being declared previously before use.
	keyType, err := b.transformTypeFor(base, others)
	if err != nil {
		return nil, err
	}

	vard := new(Key)
	vard.Type = keyType
	vard.Name = base.Name
	vard.Location = b.getLocation(base.Pos(), base.End())

	scope.Scope[base.Name] = vard
	return vard, nil
}

func (b *ParseScope) transformFunctionSliceExpr(e *ast.SliceExpr, bn *ast.BlockStmt, fn *Function, others map[string]*Package) (*SliceExpr, error) {
	var declr SliceExpr
	declr.Location = b.getLocation(e.Pos(), e.End())

	target, err := b.transformFunctionExpr(e.X, bn, fn, others)
	if err != nil {
		return nil, err
	}

	declr.Target = target

	if e.Max != nil {
		max, cerr := b.transformFunctionExpr(e.Max, bn, fn, others)
		if cerr != nil {
			return nil, cerr
		}

		declr.Max = max
	}

	if e.Low != nil {
		lowest, cerr := b.transformFunctionExpr(e.Low, bn, fn, others)
		if cerr != nil {
			return nil, cerr
		}

		declr.Lowest = lowest
	}

	if e.High != nil {
		highest, cerr := b.transformFunctionExpr(e.High, bn, fn, others)
		if err != nil {
			return nil, cerr
		}

		declr.Highest = highest
	}

	return &declr, err
}

func (b *ParseScope) transformFunctionArrayType(e *ast.ArrayType, bn *ast.BlockStmt, fn *Function, others map[string]*Package) (*List, error) {
	var declr List
	if _, ok := e.Elt.(*ast.StarExpr); ok {
		declr.IsPointer = true
	}

	// read location information(line, column, source text etc) for giving type.
	declr.Location = b.getLocation(e.Pos(), e.End())

	var err error
	declr.Type, err = b.transformFunctionExpr(e.Elt, bn, fn, others)
	return &declr, err
}

func (b *ParseScope) transformFunctionChanType(e *ast.ChanType, bn *ast.BlockStmt, fn *Function, others map[string]*Package) (*Channel, error) {
	var err error
	var declr Channel

	// read location information(line, column, source text etc) for giving type.
	declr.Location = b.getLocation(e.Pos(), e.End())

	declr.Type, err = b.transformFunctionExpr(e.Value, bn, fn, others)
	return &declr, err
}

func (b *ParseScope) transformFunctionMapType(e *ast.MapType, bn *ast.BlockStmt, fn *Function, others map[string]*Package) (*Map, error) {
	var declr Map

	var err error
	declr.KeyType, err = b.transformFunctionExpr(e.Key, bn, fn, others)
	if err != nil {
		return nil, err
	}

	declr.ValueType, err = b.transformFunctionExpr(e.Value, bn, fn, others)
	if err != nil {
		return nil, err
	}

	return &declr, nil
}

func (b *ParseScope) transformFunctionSelectorExpr(e *ast.SelectorExpr, bn *ast.BlockStmt, fn *Function, others map[string]*Package) (Expr, error) {
	switch tm := e.X.(type) {
	case *ast.Ident:
		return b.transformFunctionSelectorExprWithXIdent(tm, e, bn, fn, others)
	case *ast.CallExpr:
		return b.transformFunctionSelectorExprWithXCallExpr(tm, e, bn, fn, others)
	case *ast.IndexExpr:
		return b.transformFunctionSelectorExprWithXIndexExpr(tm, e, bn, fn, others)
	case *ast.SelectorExpr:
		return b.transformFunctionSelectorExprWithXSelector(tm, e, bn, fn, others)
	}

	return b.transformFunctionSelectorExprWithX(e.X, e, bn, fn, others)
}

func (b *ParseScope) transformFunctionSelectorExprWithXIndexExpr(base *ast.IndexExpr, e *ast.SelectorExpr, bn *ast.BlockStmt, fn *Function, others map[string]*Package) (*IndexedProperty, error) {
	target, err := b.transformFunctionIndexExpr(base, bn, fn, others)
	if err != nil {
		return nil, err
	}

	var assigned IndexedProperty
	assigned.Index = target

	targettedProperty, err := b.locateRefFromObject([]string{e.Sel.Name, e.Sel.Name}, target.Elem, others)
	if err != nil {
		return nil, err
	}

	assigned.Property = targettedProperty
	return &assigned, nil
}

func (b *ParseScope) runScopeTree(base *ast.Ident, path string) (Expr, error) {
	// Find if this scope has a parent, then attempt running through that.
	parentScope, ok := b.parentScopes[path]
	if !ok {
		return &UnknownExpr{
			File:  b.Package,
			Error: errors.New("property %q in %q not found in scope tree %q", base.Name, path),
		}, nil
	}

	// Get scope path for giving property, we need to confirm if we've already
	// seen/proc this before.
	scopedPath := strings.Join([]string{parentScope.Name, base.Name}, ".")

	// if this scopedPath has being added before, use that.
	if scopedVar, ok := parentScope.Scope[scopedPath]; ok {
		return scopedVar, nil
	}

	// if not, check if we have another parent scope then return.
	return b.runScopeTree(base, parentScope.Path)
}

func (b *ParseScope) transformFunctionSelectorExprWithXIdent(base *ast.Ident, e *ast.SelectorExpr, bn *ast.BlockStmt, fn *Function, others map[string]*Package) (Expr, error) {
	// Get the function scope map.
	scope := b.scopes[fn.Path]

	// Get scope path for giving property, we need to confirm if we've already
	// seen/proc this before.
	scopedPath := strings.Join([]string{base.Name, e.Sel.Name}, ".")

	// if this scopedPath has being added before, use that.
	if scopedVar, ok := scope.Scope[scopedPath]; ok {
		return scopedVar, nil
	}

	// if we already have variable name already scoped, then work with that
	if scopedVar, ok := scope.Scope[base.Name]; ok {
		// if the other end is nil then return scoped identity.
		if e.Sel == nil {
			return scopedVar, nil
		}

		id, err := b.locateRefFromObject([]string{e.Sel.Name}, scopedVar, others)
		if err != nil {
			return nil, err
		}

		if exprID, ok := id.(Expr); ok {
			scope.Scope[scopedPath] = exprID
			return exprID, nil
		}

		return nil, errors.New("expected function scoped property %q should also match Expr interface: %#v", e.Sel.Name, id)
	}

	// Case 1:
	//
	// If the selectors X is the same with the function receiver then we are
	// probably dealing with a call to the function's owners field.
	if base.Name == fn.ReceiverName {
		// Add giving function owner into scope for giving variable name.
		scope.Scope[base.Name] = fn.Owner

		// if the other end is nil then return owner expression.
		if e.Sel == nil {
			return fn.Owner, nil
		}

		id, err := b.locateRefFromObject([]string{e.Sel.Name}, fn.Owner, others)
		if err != nil {
			return nil, err
		}

		if exprID, ok := id.(Expr); ok {
			scope.Scope[scopedPath] = exprID
			return exprID, nil
		}

		return nil, errors.New("expected function owner property %q should also match Expr interface: %#v", e.Sel.Name, id)
	}

	// Case 2:
	//
	// If its not then we are probably dealing with a previously the functions
	// argument or returned types.
	var found bool
	var target *Parameter
	for _, arg := range fn.Arguments {
		if arg.Name == base.Name {
			target = arg
			found = true
			break
		}
	}

	// SubCase:
	//
	// We will have two cases of checks for not found:
	// 1. The identity is not part of the arguments so must be a named returned type
	// 2. Its neither the argument or returned type hence must be a third case, possible
	// a declared or assigned variable.

	// SubCase 1:
	//
	// Its not an argument, then its possible a named returned type.
	if !found {
		for _, arg := range fn.Returns {
			if arg.Name == base.Name {
				target = arg
				found = true
				break
			}
		}
	}

	// SubCase 2:
	//
	// Its not a named return type, so must be a declared variable.
	if !found {

		// This means we should have value for ast.Selector.Sel else return an error.
		if e.Sel == nil {
			return nil, errors.New("property %q within function %q not scoped through variable assigned", base.Name, fn.Name)
		}

		// Case 3:
		//
		// If we got here and still did not find this property in:
		// 1. Scoped declared variables handled in ast.AssignStmt
		// 2. Not in arguments list of function
		// 3. Not in return list of function

		// Then this is an external declared variable, possible
		// a type as well.
		if external, err := b.locateRefFromPackage(e.Sel.Name, base.Name, others); err == nil {
			scope.Scope[scopedPath] = external
			return external, nil
		}

		// Search for variables within package.
		if targetVariable, err := b.From.GetVariable(base.Name, ""); err == nil {
			if external, err := b.locateRefFromObject([]string{e.Sel.Name}, targetVariable, others); err == nil {
				scope.Scope[scopedPath] = external
				return external, nil
			}
		}

		// lets search by platform for giving variable.
		for plat := range b.Package.Platforms {
			if targetVariable, err := b.From.GetVariable(base.Name, plat); err == nil {
				if external, err := b.locateRefFromObject([]string{e.Sel.Name}, targetVariable, others); err == nil {
					scope.Scope[scopedPath] = external
					return external, nil
				}
			}
		}

		// lets search by architecture for giving variable.
		for arch := range b.Package.Archs {
			if targetVariable, err := b.From.GetVariable(base.Name, arch); err == nil {
				if external, err := b.locateRefFromObject([]string{e.Sel.Name}, targetVariable, others); err == nil {
					scope.Scope[scopedPath] = external
					return external, nil
				}
			}
		}

		// Then this is an external package alias be used to access
		// a function, type, variable.
		if targetImport, err := b.getImportFromAlias(base.Name, others); err == nil {
			importedTarget, err := b.locateRefFromPackage(e.Sel.Name, targetImport.Path, others)
			if err != nil {
				return nil, err
			}

			scope.Scope[scopedPath] = importedTarget
			return importedTarget, nil
		}

		return b.runScopeTree(base, fn.Path)
	}

	// We found a target either in the argument or named returned type.
	// Add base into scope list.
	scope.Scope[base.Name] = target

	// if the other end is nil then return target.
	if e.Sel == nil {
		return target, nil
	}

	// Create scope path for target property.
	scopedTargetPath := strings.Join([]string{target.Name, e.Sel.Name}, ".")

	// Search for desired property from underline parameter type.
	id, err := b.locateRefFromObject([]string{e.Sel.Name}, target, others)
	if err != nil {
		return nil, err
	}

	// Add giving property with base scope into scope map.
	scope.Scope[scopedTargetPath] = id

	return id, nil
}

func (b *ParseScope) transformFunctionSelectorExprWithXSelector(base *ast.SelectorExpr, parent *ast.SelectorExpr, bn *ast.BlockStmt, fn *Function, others map[string]*Package) (Expr, error) {
	// Get the function scope map.
	scope := b.scopes[fn.Path]

	// Since its a multi-selector variation then we have different cases in respect to this:
	// 1. It is a multi-select on the field values of the function owner type.
	// 2. It is a multi-select on a declared variable in the function body.
	// 3. It is a multi-select on a imported package field or type in the function body.

	parts, lastSel, err := b.getSelectorTree(nil, base, parent, others)
	if err != nil {
		return nil, err
	}

	scopedPath := strings.Join(parts, ".")
	if scopedVar, ok := scope.Scope[scopedPath]; ok {
		return scopedVar, nil
	}

	// To deal with case 1 and 2:
	root, err := b.transformFunctionIdentExpr(&ast.Ident{Name: parts[0]}, bn, fn, others)
	if err != nil {
		return nil, err
	}

	locatedTarget, err := b.locateRefFromObject(parts, root, others)
	if err != nil {
		return nil, err
	}

	scope.Scope[scopedPath] = locatedTarget

	if lastSel == nil {
		return locatedTarget, nil
	}

	procElem, err := b.transformTypeFor(lastSel.X, others)
	if err != nil {
		return nil, err
	}

	derivedElem, err := b.locateRefFromObject([]string{lastSel.Sel.Name}, procElem, others)
	if err != nil {
		return nil, err
	}

	return derivedElem, nil
}

func (b *ParseScope) transformFunctionSelectorExprWithX(base ast.Expr, parent *ast.SelectorExpr, bn *ast.BlockStmt, fn *Function, others map[string]*Package) (Expr, error) {
	// Get the function scope map.
	scope := b.scopes[fn.Path]
	if scopedVar, ok := scope.Scope[parent.Sel.Name]; ok {
		return scopedVar, nil
	}

	calledExpr, err := b.transformFunctionExpr(base, bn, fn, others)
	if err != nil {
		return nil, err
	}

	selectedTarget, err := b.locateRefFromObject([]string{parent.Sel.Name}, calledExpr, others)
	if err != nil {
		return nil, err
	}

	scope.Scope[parent.Sel.Name] = selectedTarget
	return selectedTarget, nil
}

func (b *ParseScope) transformFunctionSelectorExprWithXCallExpr(base *ast.CallExpr, parent *ast.SelectorExpr, bn *ast.BlockStmt, fn *Function, others map[string]*Package) (Expr, error) {
	// Get the function scope map.
	scope := b.scopes[fn.Path]
	if scopedVar, ok := scope.Scope[parent.Sel.Name]; ok {
		return scopedVar, nil
	}

	calledExpr, err := b.transformFunctionCallExpr(base, bn, fn, others)
	if err != nil {
		return nil, err
	}

	selectedTarget, err := b.locateRefFromObject([]string{parent.Sel.Name}, calledExpr.Func, others)
	if err != nil {
		return nil, err
	}

	scope.Scope[parent.Sel.Name] = selectedTarget
	return selectedTarget, nil
}

func (b *ParseScope) transformFunctionCompositeLitValue(src *ast.CompositeLit, bn *ast.BlockStmt, fn *Function, others map[string]*Package) (Expr, error) {
	if src.Type == nil {
		return b.transformFunctionNoTypeComposite(src, bn, fn, others)
	}

	switch elem := src.Type.(type) {
	case *ast.Ident:
		return b.transformFunctionIdentComposite(elem, src, bn, fn, others)
	case *ast.SelectorExpr:
		return b.transformFunctionSelectorComposite(elem, src, bn, fn, others)
	case *ast.StructType:
		return b.transformFunctionStructComposite(elem, src, bn, fn, others)
	case *ast.ArrayType:
		return b.transformFunctionArrayComposite(elem, src, bn, fn, others)
	case *ast.MapType:
		return b.transformFunctionMapComposite(elem, src, bn, fn, others)
	}
	return nil, errors.New("unable to handle ast.Composite type %T %#v", src.Type, src)
}

func (b *ParseScope) transformFunctionMapTypeComposite(tl *types.Map, src *ast.CompositeLit, bn *ast.BlockStmt, fn *Function, others map[string]*Package) (*DeclaredMapValue, error) {
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

		pk, err := b.transformFunctionExpr(kv.Key, bn, fn, others)
		if err != nil {
			return nil, err
		}

		pv, err := b.transformFunctionExpr(kv.Value, bn, fn, others)
		if err != nil {
			return nil, err
		}

		val.KeyValues = append(val.KeyValues, KeyValuePair{Key: pk, Value: pv})
	}

	return &val, nil
}

func (b *ParseScope) transformFunctionMapComposite(tl *ast.MapType, src *ast.CompositeLit, bn *ast.BlockStmt, fn *Function, others map[string]*Package) (*DeclaredMapValue, error) {
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

		pk, err := b.transformFunctionExpr(kv.Key, bn, fn, others)
		if err != nil {
			return nil, err
		}

		pv, err := b.transformFunctionExpr(kv.Value, bn, fn, others)
		if err != nil {
			return nil, err
		}

		val.KeyValues = append(val.KeyValues, KeyValuePair{Key: pk, Value: pv})
	}

	return &val, nil
}

func (b *ParseScope) transformFunctionNoTypeComposite(src *ast.CompositeLit, bn *ast.BlockStmt, fn *Function, others map[string]*Package) (*DeclaredValue, error) {
	var val DeclaredValue
	val.Location = b.getLocation(src.Pos(), src.End())

	if len(src.Elts) == 0 {
		return &val, nil
	}

	for _, elem := range src.Elts {
		item, err := b.transformFunctionExpr(elem, bn, fn, others)
		if err != nil {
			return nil, err
		}
		val.Values = append(val.Values, item)
	}

	return &val, nil
}

func (b *ParseScope) transformFunctionArrayComposite(tl *ast.ArrayType, src *ast.CompositeLit, bn *ast.BlockStmt, fn *Function, others map[string]*Package) (*DeclaredListValue, error) {
	var val DeclaredListValue
	val.Location = b.getLocation(src.Pos(), src.End())

	if len(src.Elts) == 0 {
		return &val, nil
	}

	for _, elem := range src.Elts {
		item, err := b.transformFunctionExpr(elem, bn, fn, others)
		if err != nil {
			return nil, err
		}
		val.Values = append(val.Values, item)
	}

	return &val, nil
}

func (b *ParseScope) transformFunctionSliceTypeComposite(tl *types.Slice, src *ast.CompositeLit, bn *ast.BlockStmt, fn *Function, others map[string]*Package) (*DeclaredListValue, error) {
	var val DeclaredListValue
	val.Location = b.getLocation(src.Pos(), src.End())

	if len(src.Elts) == 0 {
		return &val, nil
	}

	for _, elem := range src.Elts {
		item, err := b.transformFunctionExpr(elem, bn, fn, others)
		if err != nil {
			return nil, err
		}
		val.Values = append(val.Values, item)
	}

	return &val, nil
}

func (b *ParseScope) transformFunctionArrayTypeComposite(tl *types.Array, src *ast.CompositeLit, bn *ast.BlockStmt, fn *Function, others map[string]*Package) (*DeclaredListValue, error) {
	var val DeclaredListValue
	val.Length = tl.Len()
	val.Location = b.getLocation(src.Pos(), src.End())

	if len(src.Elts) == 0 {
		return &val, nil
	}

	for _, elem := range src.Elts {
		item, err := b.transformFunctionExpr(elem, bn, fn, others)
		if err != nil {
			return nil, err
		}
		val.Values = append(val.Values, item)
	}

	return &val, nil
}

func (b *ParseScope) transformFunctionStructTypeComposite(tl *types.Struct, src *ast.CompositeLit, bn *ast.BlockStmt, fn *Function, others map[string]*Package) (*DeclaredStructValue, error) {
	var val DeclaredStructValue
	val.Fields = map[string]Expr{}
	val.Location = b.getLocation(src.Pos(), src.End())

	if len(src.Elts) == 0 {
		return &val, nil
	}

	for _, elem := range src.Elts {
		switch belem := elem.(type) {
		case *ast.BasicLit:
			val.ValuesOnly = true
			member, err := b.transformFunctionExpr(belem, bn, fn, others)
			if err != nil {
				return nil, err
			}
			val.Values = append(val.Values, member)
		case *ast.KeyValueExpr:
			fieldName, ok := belem.Key.(*ast.Ident)
			if !ok {
				return nil, errors.New("ast.KeyValueExpr should be a *ast.Ident: %#v", belem.Key)
			}

			fieldVal, err := b.transformFunctionExpr(belem.Value, bn, fn, others)
			if err != nil {
				return nil, err
			}

			val.Fields[fieldName.Name] = fieldVal
		}
	}

	return &val, nil
}

func (b *ParseScope) transformFunctionStructComposite(tl *ast.StructType, src *ast.CompositeLit, bn *ast.BlockStmt, fn *Function, others map[string]*Package) (*DeclaredStructValue, error) {
	var val DeclaredStructValue
	val.Fields = map[string]Expr{}
	val.Location = b.getLocation(src.Pos(), src.End())

	if len(src.Elts) == 0 {
		return &val, nil
	}

	for _, elem := range src.Elts {
		switch belem := elem.(type) {
		case *ast.BasicLit:
			val.ValuesOnly = true
			member, err := b.transformFunctionExpr(belem, bn, fn, others)
			if err != nil {
				return nil, err
			}
			val.Values = append(val.Values, member)
		case *ast.KeyValueExpr:
			fieldName, ok := belem.Key.(*ast.Ident)
			if !ok {
				return nil, errors.New("ast.KeyValueExpr should be a *ast.Ident: %#v", belem.Key)
			}

			fieldVal, err := b.transformFunctionExpr(belem.Value, bn, fn, others)
			if err != nil {
				return nil, err
			}

			val.Fields[fieldName.Name] = fieldVal
		}
	}

	return &val, nil
}

func (b *ParseScope) transformFunctionIdentComposite(tl *ast.Ident, src *ast.CompositeLit, bn *ast.BlockStmt, fn *Function, others map[string]*Package) (Expr, error) {
	identObj := b.Info.ObjectOf(tl)
	identObjType, ok := identObj.Type().(*types.Named)
	if !ok {
		return nil, errors.New("ast.Ident for IdentComposite should have types.Named as type")
	}

	switch base := identObjType.Underlying().(type) {
	case *types.Map:
		return b.transformFunctionMapTypeComposite(base, src, bn, fn, others)
	case *types.Array:
		return b.transformFunctionArrayTypeComposite(base, src, bn, fn, others)
	case *types.Slice:
		return b.transformFunctionSliceTypeComposite(base, src, bn, fn, others)
	case *types.Struct:
		return b.transformFunctionStructTypeComposite(base, src, bn, fn, others)
	}

	return nil, errors.New("types.Ident types.Object.Underlying() for ast.CompositeLit unable to be handled: %#v", identObjType.Underlying())
}

func (b *ParseScope) transformFunctionSelectorComposite(tl *ast.SelectorExpr, src *ast.CompositeLit, bn *ast.BlockStmt, fn *Function, others map[string]*Package) (Expr, error) {
	selObj := b.Info.ObjectOf(tl.Sel)
	selType, ok := selObj.Type().(*types.Named)
	if !ok {
		return nil, errors.New("ast.SelectorExpr for SelectorComposite should have types.Named as type")
	}

	switch base := selType.Underlying().(type) {
	case *types.Map:
		return b.transformFunctionMapTypeComposite(base, src, bn, fn, others)
	case *types.Slice:
		return b.transformFunctionSliceTypeComposite(base, src, bn, fn, others)
	case *types.Struct:
		return b.transformFunctionStructTypeComposite(base, src, bn, fn, others)
	}

	return nil, errors.New("types.SelectorExpr types.Object.Underlying() for ast.CompositeLit unable to be handled: %#v", selType.Underlying())
}

//**********************************************************************************
// Type related transformations
//**********************************************************************************

func (b *ParseScope) transformTypeFor(e interface{}, others map[string]*Package) (Expr, error) {
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
	case *ast.SliceExpr:
		return b.transformSliceExpr(core, others)
	case *types.Struct:
		return b.transformStruct(core, others)
	case *ast.BasicLit:
		return b.transformBasicLit(core, others)
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
		return b.handleFunctionLit(core, others)
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

	return nil, errors.New("unable to handle type: %#v", e)
}

func (b *ParseScope) transformTypeAssertExpr(src *ast.TypeAssertExpr, others map[string]*Package) (*TypeAssert, error) {
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

	return &ts, nil
}

func (b *ParseScope) transformKeyValuePair(src *ast.KeyValueExpr, others map[string]*Package) (*KeyValuePair, error) {
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

func (b *ParseScope) transformUnaryExpr(src *ast.UnaryExpr, others map[string]*Package) (Expr, error) {
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

func (b *ParseScope) transformBinaryExpr(src *ast.BinaryExpr, others map[string]*Package) (*BinaryExpr, error) {
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

func (b *ParseScope) transformParenExpr(src *ast.ParenExpr, others map[string]*Package) (*ParenExpr, error) {
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

	indexed, err := b.transformTypeFor(src.Index, others)
	if err != nil {
		return nil, err
	}

	val.Index = indexed

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

func (b *ParseScope) transformBasicLit(e *ast.BasicLit, others map[string]*Package) (*BaseValue, error) {
	var bm BaseValue
	bm.Value = e.Value
	bm.Location = b.getLocation(e.Pos(), e.End())
	return &bm, nil
}

func (b *ParseScope) getBaseType(name string) (Expr, error) {
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
	case "byte", "rune", "string":
		return BaseFor(name), nil
	case "float32", "float64":
		return BaseFor(name), nil
	case "complex128", "complex64":
		return BaseFor(name), nil
	}
	return nil, errors.New("unknown base type %q", name)
}

func (b *ParseScope) transformIdent(e *ast.Ident, others map[string]*Package) (Expr, error) {
	if bd, err := b.getBaseType(e.Name); err == nil {
		return bd, err
	}

	if e.Obj == nil {
		bm := BaseFor(e.Name)
		bm.Location = b.getLocation(e.Pos(), e.End())
		return bm, nil
	}

	if bo := b.Info.ObjectOf(e); bo != nil {
		to := bo.Type()
		if identity, err := b.identityType(to, others); err == nil {
			bm, err := b.locateReferencedExpr(identity, others)
			return bm, err
		}
	}

	ref := strings.Join([]string{b.From.Name, e.Name}, ".")
	if b, err := b.From.GetReference(ref); err == nil {
		return b, nil
	}

	return BaseFor(e.Name), nil
}

func (b *ParseScope) transformVarObject(src *types.Var, others map[string]*Package) (Expr, error) {
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

func (b *ParseScope) transformAssignedValueSpec(spec *ast.ValueSpec, src *ast.Ident, others map[string]*Package) (Expr, error) {
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

func (b *ParseScope) transformCompositeLitValue(src *ast.CompositeLit, others map[string]*Package) (Expr, error) {
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

func (b *ParseScope) transformNoTypeComposite(src *ast.CompositeLit, others map[string]*Package) (*DeclaredValue, error) {
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
	val.Fields = map[string]Expr{}
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
	val.Fields = map[string]Expr{}
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

func (b *ParseScope) transformIdentComposite(tl *ast.Ident, src *ast.CompositeLit, others map[string]*Package) (Expr, error) {
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

func (b *ParseScope) transformSelectorComposite(tl *ast.SelectorExpr, src *ast.CompositeLit, others map[string]*Package) (Expr, error) {
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

func (b *ParseScope) transformBasic(e *types.Basic, others map[string]*Package) (*Base, error) {
	bm := BaseFor(e.Name())
	return &bm, nil
}

func (b *ParseScope) transformObject(e types.Object, others map[string]*Package) (Expr, error) {
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

func (b *ParseScope) transformPointerWithObject(e *types.Pointer, vard types.Object, others map[string]*Package) (Expr, error) {
	var err error
	var declr Pointer
	declr.Elem, err = b.transformTypeFor(e.Elem(), others)
	return declr, err
}

func (b *ParseScope) transformArrayWithObject(e *types.Array, vard types.Object, others map[string]*Package) (*List, error) {
	var declr List
	if _, ok := e.Elem().(*types.Pointer); ok {
		declr.IsPointer = true
	}

	var err error
	declr.Type, err = b.transformTypeFor(e.Elem(), others)
	return &declr, err
}

func (b *ParseScope) transformSliceWithObject(e *types.Slice, vard types.Object, others map[string]*Package) (*List, error) {
	var declr List
	declr.IsSlice = true

	if _, ok := e.Elem().(*types.Pointer); ok {
		declr.IsPointer = true
	}

	var err error
	declr.Type, err = b.transformTypeFor(e.Elem(), others)
	return &declr, err
}

func (b *ParseScope) transformChanWithObject(e *types.Chan, vard types.Object, others map[string]*Package) (*Channel, error) {
	return b.transformChan(e, others)
}

func (b *ParseScope) transformBasicWithObject(e *types.Basic, vard types.Object, others map[string]*Package) (*Base, error) {
	return b.transformBasic(e, others)
}

func (b *ParseScope) transformInterfaceWithObject(e *types.Interface, vard types.Object, others map[string]*Package) (Expr, error) {
	if identity, err := b.identityType(e, others); err == nil {
		return b.locateReferencedExpr(identity, others)
	}
	return b.transformTypeFor(e, others)
}

func (b *ParseScope) transformStructWithObject(e *types.Struct, vard types.Object, others map[string]*Package) (Expr, error) {
	if identity, err := b.identityType(e, others); err == nil {
		return b.locateReferencedExpr(identity, others)
	}
	return b.transformTypeFor(e, others)
}

func (b *ParseScope) transformPackagelessObject(te types.Type, obj types.Object, others map[string]*Package) (Expr, error) {
	switch tm := te.(type) {
	case *types.Basic:
		return b.transformTypeFor(tm, others)
	case *types.Named:
		return b.transformPackagelessNamedWithObject(tm, obj, others)
	}
	return nil, errors.New("package-less type: %#v", te)
}

func (b *ParseScope) transformPackagelessNamedWithObject(e *types.Named, obj types.Object, others map[string]*Package) (Expr, error) {
	switch tm := e.Underlying().(type) {
	case *types.Struct:
		return b.transformStructWithNamed(tm, e, others)
	case *types.Interface:
		return b.transformInterfaceWithNamed(tm, e, others)
	}
	return nil, errors.New("package-less named type: %#v", e)
}

func (b *ParseScope) transformNamedWithObject(e *types.Named, obj types.Object, others map[string]*Package) (Expr, error) {
	if identity, err := b.identityType(e, others); err == nil {
		return b.locateReferencedExpr(identity, others)
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

func (b *ParseScope) transformNamed(e *types.Named, others map[string]*Package) (Expr, error) {
	if identity, err := b.identityType(e, others); err == nil {
		return b.locateReferencedExpr(identity, others)
	}
	return b.transformTypeFor(e.Underlying(), others)
}

func (b *ParseScope) transformSignatureWithObject(signature *types.Signature, obj types.Object, others map[string]*Package) (Function, error) {
	var fn Function
	fn.IsVariadic = signature.Variadic()
	fn.Name = String(10)
	fn.Path = strings.Join([]string{b.From.Name, fn.Name}, ".")

	if params := signature.Results(); params != nil {
		for i := 0; i < params.Len(); i++ {
			vard := params.At(i)
			ft, err := b.transformObject(vard, others)
			if err != nil {
				return fn, err
			}

			field := new(Parameter)
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

			field := new(Parameter)
			field.Type = ft
			field.Name = vard.Name()
			fn.Returns = append(fn.Returns, field)
		}
	}

	return fn, nil
}

func (b *ParseScope) locateStructWithObject(e *types.Struct, named *types.Named, obj types.Object, others map[string]*Package) (Expr, error) {
	if identity, err := b.identityType(e, others); err == nil {
		return b.locateReferencedExpr(identity, others)
	}

	if named.Obj().Pkg() == nil {
		return b.transformPackagelessObject(named, named.Obj(), others)
	}

	myPackage := obj.Pkg()

	targetPackage, ok := others[myPackage.Path()]
	if !ok {
		return nil, errors.New("Unable to find target package %q", obj.Pkg().Path())
	}

	ref := strings.Join([]string{targetPackage.Name, named.Obj().Name()}, ".")
	if target, ok := targetPackage.Structs[ref]; ok {
		return target, nil
	}

	imported := myPackage.Imports()
	for _, stage := range imported {
		targetPackage, ok := others[stage.Path()]
		if !ok {
			return nil, errors.New("Unable to find imported package %q", stage.Path())
		}

		ref := strings.Join([]string{targetPackage.Name, named.Obj().Name()}, ".")
		if target, ok := targetPackage.Structs[ref]; ok {
			return target, nil
		}
	}

	return nil, errors.New("Unable to find target struct %q from package %q or its imported set", named.Obj().Name(), named.Obj().Pkg().Path())
}

func (b *ParseScope) locateReferencedExpr(location typeExpr, others map[string]*Package) (Expr, error) {
	if bd, err := b.getBaseType(location.Text); err == nil {
		return bd, err
	}

	targetPackage, ok := others[location.Package]
	if !ok {
		return nil, errors.New("unable to find package for %q", location.Package)
	}

	if found, err := targetPackage.GetReferenceByArchs(location.Text, b.Package.Archs, b.Package.Cgo); err == nil {
		return found, nil
	}

	return &UnknownExpr{
		File:  b.Package,
		Error: errors.New("unable to locate referenced type: %#v", location),
	}, nil
}

func (b *ParseScope) locateInterfaceWithObject(e *types.Interface, named *types.Named, obj types.Object, others map[string]*Package) (Expr, error) {
	if identity, err := b.identityType(e, others); err == nil {
		return b.locateReferencedExpr(identity, others)
	}

	myPackage := obj.Pkg()
	targetPackage, ok := others[myPackage.Path()]
	if !ok {
		return nil, errors.New("Unable to find target package %q", obj.Pkg().Path())
	}

	ref := strings.Join([]string{targetPackage.Name, named.Obj().Name()}, ".")
	if target, ok := targetPackage.Interfaces[ref]; ok {
		return target, nil
	}

	imported := myPackage.Imports()
	for _, stage := range imported {
		targetPackage, ok := others[stage.Path()]
		if !ok {
			return nil, errors.New("Unable to find imported package %q", stage.Path())
		}

		ref := strings.Join([]string{targetPackage.Name, named.Obj().Name()}, ".")
		if target, ok := targetPackage.Interfaces[ref]; ok {
			return target, nil
		}
	}

	if named.Obj().Pkg() == nil {
		return b.transformPackagelessObject(named, named.Obj(), others)
	}

	return nil, errors.New("Unable to find target Interface %q from package %q or its imported set", named.Obj().Name(), named.Obj().Pkg().Path())
}

func (b *ParseScope) transformStructWithNamed(e *types.Struct, named *types.Named, others map[string]*Package) (Expr, error) {
	if identity, err := b.identityType(e, others); err == nil {
		return b.locateReferencedExpr(identity, others)
	}
	return b.transformTypeFor(e, others)
}

func (b *ParseScope) transformInterfaceWithNamed(e *types.Interface, named *types.Named, others map[string]*Package) (Expr, error) {
	if identity, err := b.identityType(e, others); err == nil {
		return b.locateReferencedExpr(identity, others)
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

func (b *ParseScope) transformInterfaceType(e *ast.InterfaceType, others map[string]*Package) (*Interface, error) {
	var declr Interface
	declr.Name = String(10)
	declr.Path = strings.Join([]string{b.From.Name, declr.Name}, ".")
	declr.Methods = map[string]*Function{}

	// read location information(line, column, source text etc) for giving type.
	declr.Location = b.getLocation(e.Pos(), e.End())

	if e.Methods == nil {
		return &declr, nil
	}

	for _, field := range e.Methods.List {
		if len(field.Names) == 0 {
			if err := b.handleEmbeddedInterface(declr.Name, field, field.Type, others, &declr); err != nil {
				return nil, err
			}
			continue
		}

		for _, name := range field.Names {
			fn, err := b.handleFunctionFieldWithName(declr.Name, field, name, field.Type)
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
	declr.Fields = map[string]*Field{}
	declr.Name = String(10)
	declr.Path = strings.Join([]string{b.From.Name, declr.Name}, ".")

	// read location information(line, column, source text etc) for giving type.
	declr.Location = b.getLocation(e.Pos(), e.End())

	if e.Fields == nil {
		return &declr, nil
	}

	for _, field := range e.Fields.List {
		if len(field.Names) == 0 {
			parsedField, err := b.handleField(declr.Name, field, field.Type)
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
			parsedField, err := b.handleFieldWithName(declr.Name, field, fieldName, field.Type)
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

func (b *ParseScope) transformStarExpr(e *ast.StarExpr, others map[string]*Package) (*Pointer, error) {
	var err error
	var declr Pointer

	// read location information(line, column, source text etc) for giving type.
	declr.Location = b.getLocation(e.Pos(), e.End())

	declr.Elem, err = b.transformTypeFor(e.X, others)
	return &declr, err
}

func (b *ParseScope) transformArrayType(e *ast.ArrayType, others map[string]*Package) (*List, error) {
	var declr List
	if _, ok := e.Elt.(*ast.StarExpr); ok {
		declr.IsPointer = true
	}

	// read location information(line, column, source text etc) for giving type.
	declr.Location = b.getLocation(e.Pos(), e.End())

	var err error
	declr.Type, err = b.transformTypeFor(e.Elt, others)
	return &declr, err
}

func (b *ParseScope) transformSliceExpr(e *ast.SliceExpr, others map[string]*Package) (*SliceExpr, error) {
	var declr SliceExpr
	declr.Location = b.getLocation(e.Pos(), e.End())

	target, err := b.transformTypeFor(e.X, others)
	if err != nil {
		return nil, err
	}

	declr.Target = target

	if e.Low != nil {
		max, cerr := b.transformTypeFor(e.Max, others)
		if cerr != nil {
			return nil, cerr
		}

		declr.Max = max
	}

	if e.Low != nil {
		lowest, cerr := b.transformTypeFor(e.Low, others)
		if cerr != nil {
			return nil, cerr
		}

		declr.Lowest = lowest
	}

	if e.High != nil {
		highest, cerr := b.transformTypeFor(e.High, others)
		if cerr != nil {
			return nil, cerr
		}

		declr.Highest = highest
	}

	return &declr, err
}

func (b *ParseScope) transformChanType(e *ast.ChanType, others map[string]*Package) (*Channel, error) {
	var err error
	var declr Channel

	// read location information(line, column, source text etc) for giving type.
	declr.Location = b.getLocation(e.Pos(), e.End())

	declr.Type, err = b.transformTypeFor(e.Value, others)
	return &declr, err
}

func (b *ParseScope) transformMapType(e *ast.MapType, others map[string]*Package) (*Map, error) {
	var declr Map

	var err error
	declr.KeyType, err = b.transformTypeFor(e.Key, others)
	if err != nil {
		return nil, err
	}

	declr.ValueType, err = b.transformTypeFor(e.Value, others)
	if err != nil {
		return nil, err
	}

	return &declr, nil
}

func (b *ParseScope) transformFuncType(e *ast.FuncType, others map[string]*Package) (*Function, error) {
	var declr Function
	declr.Name = String(10)
	declr.Path = strings.Join([]string{b.From.Name, declr.Name}, ".")

	// read location information(line, column, source text etc) for giving type.
	declr.Location = b.getLocation(e.Pos(), e.End())

	if e.Params != nil {
		var params []*Parameter
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
		var params []*Parameter
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

func (b *ParseScope) transformPointer(e *types.Pointer, others map[string]*Package) (*Pointer, error) {
	var err error
	var declr Pointer
	declr.Elem, err = b.transformTypeFor(e.Elem(), others)
	return &declr, err
}

func (b *ParseScope) transformSignature(signature *types.Signature, others map[string]*Package) (*Function, error) {
	var fn Function
	fn.IsVariadic = signature.Variadic()
	fn.Name = String(10)
	fn.Path = strings.Join([]string{b.From.Name, fn.Name}, ".")

	if params := signature.Results(); params != nil {
		for i := 0; i < params.Len(); i++ {
			vard := params.At(i)
			ft, err := b.transformObject(vard, others)
			if err != nil {
				return nil, err
			}

			field := new(Parameter)
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

			field := new(Parameter)
			field.Type = ft
			field.Name = vard.Name()
			fn.Returns = append(fn.Returns, field)
		}
	}

	return &fn, nil
}

func (b *ParseScope) transformChan(e *types.Chan, others map[string]*Package) (*Channel, error) {
	var err error
	var declr Channel
	declr.Type, err = b.transformTypeFor(e.Elem(), others)
	return &declr, err
}

func (b *ParseScope) transformArray(e *types.Array, others map[string]*Package) (*List, error) {
	var declr List
	if _, ok := e.Elem().(*types.Pointer); ok {
		declr.IsPointer = true
	}

	var err error
	declr.Type, err = b.transformTypeFor(e.Elem(), others)
	return &declr, err
}

func (b *ParseScope) transformSlice(e *types.Slice, others map[string]*Package) (*List, error) {
	var declr List
	declr.IsSlice = true

	if _, ok := e.Elem().(*types.Pointer); ok {
		declr.IsPointer = true
	}

	var err error
	declr.Type, err = b.transformTypeFor(e.Elem(), others)
	return &declr, err
}

func (b *ParseScope) transformMap(e *types.Map, others map[string]*Package) (*Map, error) {
	var declr Map

	var err error
	declr.KeyType, err = b.transformTypeFor(e.Key(), others)
	if err != nil {
		return nil, err
	}

	declr.ValueType, err = b.transformTypeFor(e.Elem(), others)
	if err != nil {
		return nil, err
	}

	return &declr, nil
}

func (b *ParseScope) transformStruct(e *types.Struct, others map[string]*Package) (Expr, error) {
	if identity, err := b.identityType(e, others); err == nil {
		return b.locateReferencedExpr(identity, others)
	}

	var declr Struct
	declr.Name = String(10)
	declr.Path = strings.Join([]string{b.From.Name, declr.Name}, ".")
	declr.Fields = map[string]*Field{}

	for i := 0; i < e.NumFields(); i++ {
		field := e.Field(i)
		elem, err := b.transformObject(field, others)
		if err != nil {
			return declr, err
		}

		fl := new(Field)
		fl.Tags = getTags(e.Tag(i))
		fl.Type = elem
		fl.Name = field.Name()
		declr.Fields[fl.Name] = fl
	}

	if err := declr.Resolve(others); err != nil {
		return nil, err
	}

	return &declr, nil
}

func (b *ParseScope) transformInterface(e *types.Interface, others map[string]*Package) (Expr, error) {
	if identity, err := b.identityType(e, others); err == nil {
		return b.locateReferencedExpr(identity, others)
	}

	var declr Interface
	declr.Name = String(10)
	declr.Path = strings.Join([]string{b.From.Name, declr.Name}, ".")
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

func (b *ParseScope) transformSelectorExpr(parent *ast.SelectorExpr, others map[string]*Package) (Expr, error) {
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

func (b *ParseScope) transformSelectorWithComposite(cp *ast.CompositeLit, e *ast.SelectorExpr, others map[string]*Package) (Expr, error) {
	switch baseTarget := cp.Type.(type) {
	case *ast.Ident:
		element, err := b.transformTypeFor(baseTarget, others)
		if err != nil {
			return nil, err
		}

		return b.locateRefFromObject([]string{e.Sel.Name}, element, others)
	case *ast.SelectorExpr:
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

func (b *ParseScope) transformSelectorExprWithIdent(me *ast.Ident, e *ast.SelectorExpr, others map[string]*Package) (Expr, error) {
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

	ref := strings.Join([]string{b.From.Name, me.Name}, ".")
	if target, err := b.From.GetReferenceByArchs(ref, b.Package.Archs, b.Package.Cgo); err == nil {
		return target, nil
	}

	return &UnknownExpr{
		Error: errors.New("unable to locate reference %q", ref),
		File:  b.Package,
	}, nil
}

func (b *ParseScope) getElementFromPackageWithObject(target *ast.Ident) (Expr, error) {
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

func (b *ParseScope) transformSelectorExprWithSelector(next *ast.SelectorExpr, parent *ast.SelectorExpr, others map[string]*Package) (Expr, error) {
	parts, lastX, err := b.getSelectorTree(nil, next, parent, others)
	if err != nil {
		return nil, err
	}

	// We need to find specific import that uses giving name as import alias.
	if imp, ok := b.Package.Imports[parts[0]]; ok {
		return b.locateRefFromPathSeries(parts[1:], imp.Path, others)
	}

	// Are we dealing with an internal package object (variable, struct, interface, function) ?
	ref := strings.Join([]string{b.From.Name, parts[0]}, ".")
	targetRef, err := b.From.GetReferenceByArchs(ref, b.Package.Archs, b.Package.Cgo)
	if err != nil {
		return nil, errors.Wrap(err, "unable to find import with alias %q", parts[0])
	}

	pelem, err := b.locateRefFromObject(parts[1:], targetRef, others)
	if err != nil {
		return nil, err
	}

	if lastX == nil {
		return pelem, nil
	}

	procElem, err := b.transformTypeFor(lastX.X, others)
	if err != nil {
		return nil, err
	}

	derivedElem, err := b.locateRefFromObject([]string{lastX.Sel.Name}, procElem, others)
	if err != nil {
		return nil, err
	}

	return derivedElem, nil
}

func (b *ParseScope) getImportFromAlias(aliasName string, others map[string]*Package) (Import, error) {
	if imported, ok := b.Package.Imports[aliasName]; ok {
		return imported, nil
	}
	return Import{}, errors.New("unable to find import package for alias %q", aliasName)
}

func (b *ParseScope) getPackageFromAlias(aliasName string, others map[string]*Package) (*Package, error) {
	if imported, ok := b.Package.Imports[aliasName]; ok {
		if pkg, ok := others[imported.Path]; ok {
			return pkg, nil
		}
	}
	return nil, errors.New("unable to find import package for alias %q", aliasName)
}

func (b *ParseScope) getSelectorTree(parts []string, next *ast.SelectorExpr, parent *ast.SelectorExpr, others map[string]*Package) ([]string, *ast.SelectorExpr, error) {
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

	// if it's not then we stop here.
	if !ok {
		return parts, next, nil
	}

	// finally add the target name of expected next selector as the ast is done reverse.
	parts = append(parts, nextIdent.Name)

	if len(parts) == 2 {
		tmp := parts[0]
		parts[0] = parts[1]
		parts[1] = tmp
		return parts, nil, nil
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

	return parts, nil, nil
}

func (b *ParseScope) locateRefFromPathSeries(parts []string, pkg string, others map[string]*Package) (Expr, error) {
	base, err := b.locateRefFromPackage(parts[0], pkg, others)
	if err != nil {
		return nil, err
	}

	return b.locateRefFromObject(parts[1:], base, others)
}

func (b *ParseScope) locateRefFromObject(parts []string, target Expr, others map[string]*Package) (Expr, error) {
	if target == nil {
		//return nil, errors.New("targets %q can not be nil", parts)
		return &UnknownExpr{
			File:  b.Package,
			Error: errors.New("targets %q can not be nil", parts),
		}, nil
	}

	if len(parts) == 0 {
		return target, nil
	}

	switch item := target.(type) {
	case *UnknownExpr:
		return item, nil
	case *Value:
		return b.locateRefFromObject(parts[1:], item.Value, others)
	case *Key:
		return b.locateRefFromObject(parts, item.Type, others)
	case *Pointer:
		return b.locateRefFromObject(parts, item.Elem, others)
	case *ParenExpr:
		return b.locateRefFromObject(parts, item.Elem, others)
	case Parameter:
		return b.locateRefFromObject(parts[1:], item.Type, others)
	case *Parameter:
		return b.locateRefFromObject(parts[1:], item.Type, others)
	case Field:
		return b.locateRefFromObject(parts[1:], item.Type, others)
	case *Field:
		return b.locateRefFromObject(parts[1:], item.Type, others)
	case *Interface:
		if fn, ok := item.Methods[parts[0]]; ok {
			return b.locateRefFromObject(parts[1:], fn, others)
		}
		if fn, ok := item.Composes[parts[0]]; ok {
			return b.locateRefFromObject(parts[1:], fn, others)
		}
		//return nil, errors.New("unable to locate giving method from interface: %+q", parts)
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
			return b.locateRefFromObject(parts[1:], fl, others)
		}
		for name, fl := range item.Embeds {
			if name != parts[0] {
				continue
			}
			return b.locateRefFromObject(parts[1:], fl, others)
		}
		//return nil, errors.New("unable to locate giving method or field from struct: %+q", parts)
	case *Type:
		if fn, ok := item.Methods[parts[0]]; ok {
			return b.locateRefFromObject(parts[1:], fn, others)
		}
		//return nil, errors.New("unable to locate giving method from named type: %+q", parts)
	case *Variable:
		return b.locateRefFromObject(parts[1:], item.Type, others)
	}

	//return nil, errors.New("unable to locate any of provided set %+q in %T", parts, target)
	return &UnknownExpr{
		File:  b.Package,
		Error: errors.New("unable to locate any of provided set %+q in %T", parts, target),
	}, nil
}

func (b *ParseScope) locateRefFromPackage(target string, pkg string, others map[string]*Package) (Expr, error) {
	if targetPackage, ok := others[pkg]; ok {
		ref := strings.Join([]string{targetPackage.Name, target}, ".")
		if tm, err := targetPackage.GetReferenceByArchs(ref, b.Package.Archs, b.Package.Cgo); err == nil {
			return tm, nil
		}
		return nil, errors.New("failed to find %q in %q", target, pkg)
	}

	return nil, errors.New("unable to find package %q in indexed", pkg)
}
