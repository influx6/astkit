package compiler

import "golang.org/x/tools/go/loader"

// ExprType defines a int type used to represent giving
// type of expressions such as assignment, multiplication,
// division, bracket closing, ...etc.
type ExprType int

// Resolvable defines an interface which exposes a method for
// resolution of internal operations.
type Resolvable interface {
	Resolve(map[string]*Package) error
}

// Expr defines a interface exposing a giving method.
type Expr interface {
	Resolvable

	Ref() string
	Loc() *Location
}

// ResolverFn defines a function type representing
// a function taking in a map of packages, returning
// an error.
type ResolverFn func(map[string]*Package) error

// Location embodies important data relating to
// the location, column and length of declared
// statement or expression within a source file.
type Location struct {
	End       int
	File      string
	Begin     int
	Length    int
	Line      int
	LineEnd   int
	Column    int
	ColumnEnd int
	Source    string
}

// Loc implements the Expr interface.
func (l Location) Loc() Location {
	return l
}

// Ref stores giving RefName value for  type.
type Ref struct {
	RefName string
}

// Ref implements the Expr interface.
func (f Ref) Ref() string {
	return f.RefName
}

// DocText embodies a file level text not
// associated with any declaration exiting
// within a file.
type DocText struct {
	Location
	Text string
}

// Tag embodies a field tag and it's value declared
// for a struct field.
type Tag struct {
	Name  string
	Value string
	Text  string
	Meta  []string
}

// Annotation embodies an annotation declaration made
// in regards to a declared struct, type or interface
// declaration either within declaration commentary or
// within source files.
type Annotation struct {
	Name     string
	Template string
	After    []string
	Flags    []string
	Params   map[string]string
}

// Annotated defines a embeddable struct which declared
// a single field that contains all associated declared
// annotations.
type Annotated struct {
	Annotations []Annotation
}

// Doc represents the associated documentation for
// a giving package or type declaration. It contains
// the main text which is the first paragraph of the
// commentary and the extra commentary which are seperated
// by 2 newline spacing.
type Doc struct {
	Location

	Text  string
	Parts []DocText
}

// Meta defines the basic package related information
// attached to a giving structure declaration or variable
// pointing to it's origin of declaration.
type Meta struct {
	Doc  Doc
	Name string
	Path string
}

// Import embodies an import declaration within a package
// file. It contains the path, the filesystem directory
// location and alias used.
type Import struct {
	Location

	Runtime bool
	Alias   string
	Path    string
	Dir     string
	Docs    []Doc
}

// Expression provides a generic structure used to represent
// statements, special characters, symbols and operations generated
// from source code.
type Expression struct {
	Location

	Value    string
	Type     ExprType
	Children []Expression
}

// Ref returns an empty string, has Expression
// has no giving reference string.
func (e Expression) Ref() string {
	return ""
}

// PackageFile defines a giving package file with it's associated
// definitions, constructs and declarations. It provides the
// one-to-one relation of a parsed package.
type PackageFile struct {
	Name    string
	File    string
	Dir     string
	Docs    []Doc
	Imports map[string]Import
}

// Package embodies a parsed Golang/Go based package,
// with it's information and package files, which embody
// it's declarations, types and constructs.
type Package struct {
	Meta *loader.PackageInfo `json:"-"`

	Name       string
	Docs       []Doc
	Blanks     []Variable
	Constants  []Constant
	Variables  []Variable
	Types      map[string]*Type
	Structs    map[string]*Struct
	Interfaces map[string]*Interface
	Maps       map[string]*Map
	Slices     map[string]*Slice
	Functions  map[string]*Function
	Methods    map[string]*Function
	Depends    map[string]*Package
	Files      map[string]*PackageFile
}

// Add adds giving declaration into package declaration
// types according to it's class.
func (p *Package) Add(obj interface{}) error {
	switch elem := obj.(type) {
	case Doc:
		p.Docs = append(p.Docs, elem)
	case *Package:
		p.Depends[elem.Name] = elem
	case *Type:
		p.Types[elem.Name] = elem
	case *Interface:
		p.Interfaces[elem.Name] = elem
	case *Struct:
		p.Structs[elem.Name] = elem
	case *Function:
		if elem.IsMethod {
			p.Methods[elem.Name] = elem
		} else {
			p.Functions[elem.Name] = elem
		}
	case *Map:
		p.Maps[elem.Name] = elem
	case *Slice:
		p.Slices[elem.Name] = elem
	case Variable:
		if elem.Blank {
			p.Blanks = append(p.Blanks, elem)
		} else {
			p.Variables = append(p.Variables, elem)
		}
	case Constant:
		p.Constants = append(p.Constants, elem)
	}
	return nil
}

// Resolve takes the list of indexed packages to internal structures
// to resolve imported or internal types that they reference. This is
// used to ensure all package structures have direct link to parsed
// type.
func (p *Package) Resolve(indexed map[string]*Package) error {
	for _, blank := range p.Blanks {
		if err := blank.Resolve(indexed); err != nil {
			return err
		}
	}
	for _, consts := range p.Constants {
		if err := consts.Resolve(indexed); err != nil {
			return err
		}
	}
	for _, vars := range p.Variables {
		if err := vars.Resolve(indexed); err != nil {
			return err
		}
	}
	for _, tp := range p.Types {
		if err := tp.Resolve(indexed); err != nil {
			return err
		}
	}
	for _, str := range p.Structs {
		if err := str.Resolve(indexed); err != nil {
			return err
		}
	}
	for _, itr := range p.Interfaces {
		if err := itr.Resolve(indexed); err != nil {
			return err
		}
	}
	for _, fn := range p.Functions {
		if err := fn.Resolve(indexed); err != nil {
			return err
		}
	}
	return nil
}

// Field represents field types and names
// declared as part of a types's properties.
type Field struct {
	Ref
	Location

	// Docs are the documentation related to giving
	// parameter within source.
	Docs []Doc

	// Path represents the giving full qualified package path name
	// and type name of type in format: PackagePath.TypeName.
	Path string

	// Name represents the name of giving interface.
	Name string

	// Tags contains a map of all declared tags for giving fields.
	Tags map[string]Tag

	// Meta provides associated package  and commentary information related to
	// giving type.
	Meta Meta

	// Type sets the value object/declared type.
	Type interface{}

	// TypeName represents the type name of object type.
	TypeName string

	// resolver provides a means of the indexer to provide a custom resolving
	// function which will run internal logic to set giving values
	// appropriately during resolution of types.
	resolver ResolverFn
}

// Resolve takes the list of indexed packages to internal structures
// to resolve imported or internal types that they reference. This is
// used to ensure all package structures have direct link to parsed
// type.
func (p *Field) Resolve(indexed map[string]*Package) error {
	if p.resolver != nil {
		if err := p.resolver(indexed); err != nil {
			return err
		}
	}
	return nil
}

// Parameter represents argument and return types
// provided to a function, method or function type
// declaration.
type Parameter struct {
	Location

	// Docs are the documentation related to giving
	// parameter within source.
	Docs []Doc

	// Name represents the name of giving interface.
	Name string

	// Type sets the value object/declared type.
	Type interface{}

	// TypeName represents the type name of object type.
	TypeName string

	// IsVariadic indicates if giving parameter is variadic.
	IsVariadic bool

	// Meta provides associated package  and commentary information related to
	// giving type.
	Meta Meta

	// resolver provides a means of the indexer to provide a custom resolving
	// function which will run internal logic to set giving values
	// appropriately during resolution of types.
	resolver ResolverFn
}

// Resolve takes the list of indexed packages to internal structures
// to resolve imported or internal types that they reference. This is
// used to ensure all package structures have direct link to parsed
// type.
func (p *Parameter) Resolve(indexed map[string]*Package) error {
	if p.resolver != nil {
		if err := p.resolver(indexed); err != nil {
			return err
		}
	}
	return nil
}

// Map embodies a giving map type with
// an associated name, value and key type.
type Map struct {
	Ref
	Location

	// Path represents the giving full qualified package path name
	// and type name of type in format: PackagePath.TypeName.
	Path string

	// Name represents the name of giving interface.
	Name string

	// Exported is used to indicate if type is exported or not.
	Exported bool

	// KeyType sets the key type for giving map type.
	KeyType interface{}

	// ValueType sets the value type for giving map type.
	ValueType interface{}

	// Meta provides associated package  and commentary information related to
	// giving type.
	Meta Meta

	// resolver provides a means of the indexer to provide a custom resolving
	// function which will run internal logic to set giving values
	// appropriately during resolution of types.
	resolver ResolverFn
}

// Resolve takes the list of indexed packages to internal structures
// to resolve imported or internal types that they reference. This is
// used to ensure all package structures have direct link to parsed
// type.
func (p *Map) Resolve(indexed map[string]*Package) error {
	if p.resolver != nil {
		if err := p.resolver(indexed); err != nil {
			return err
		}
	}
	return nil
}

// Slice embodies a giving slice type with
// an associated name and type.
type Slice struct {
	Ref
	Location

	// Path represents the giving full qualified package path name
	// and type name of type in format: PackagePath.TypeName.
	Path string

	// Name represents the name of giving interface.
	Name string

	// Exported is used to indicate if type is exported or not.
	Exported bool

	// Type sets the value object/declared type.
	Type interface{}

	// TypeName represents the type name of object type.
	TypeName string

	// Meta provides associated package  and commentary information related to
	// giving type.
	Meta Meta

	// resolver provides a means of the indexer to provide a custom resolving
	// function which will run internal logic to set giving values
	// appropriately during resolution of types.
	resolver ResolverFn
}

// Resolve takes the list of indexed packages to internal structures
// to resolve imported or internal types that they reference. This is
// used to ensure all package structures have direct link to parsed
// type.
func (p *Slice) Resolve(indexed map[string]*Package) error {
	if p.resolver != nil {
		if err := p.resolver(indexed); err != nil {
			return err
		}
	}
	return nil
}

// Channel embodies a channel type declared
// with a golang package.
type Channel struct {
	Ref
	Location

	// Path represents the giving full qualified package path name
	// and type name of type in format: PackagePath.TypeName.
	Path string

	// Name represents the name of giving interface.
	Name string

	// Exported is used to indicate if type is exported or not.
	Exported bool

	// Meta provides associated package  and commentary information related to
	// giving type.
	Meta Meta

	// Type sets the value object/declared type.
	Type interface{}

	// TypeName represents the type name of object type.
	TypeName string

	// resolver provides a means of the indexer to provide a custom resolving
	// function which will run internal logic to set giving values
	// appropriately during resolution of types.
	resolver ResolverFn
}

// Resolve takes the list of indexed packages to internal structures
// to resolve imported or internal types that they reference. This is
// used to ensure all package structures have direct link to parsed
// type.
func (p *Channel) Resolve(indexed map[string]*Package) error {
	if p.resolver != nil {
		if err := p.resolver(indexed); err != nil {
			return err
		}
	}
	return nil
}

// Variable embodies data related to declared
// variable.
type Variable struct {
	Ref
	Location

	// Type sets the value object/declared type.
	Type interface{}

	// TypeName represents the type name of object type.
	TypeName string

	// Path represents the giving full qualified package path name
	// and type name of type in format: PackagePath.TypeName.
	Path string

	// Name represents the name of giving interface.
	Name string

	// Exported is used to indicate if type is exported or not.
	Exported bool

	// Blank is used to indicate if variable name is blank.
	Blank bool

	// IsShortHand is used to indicate if variable is declared
	// in golang short hand or normal format.
	IsShortHand bool

	// Meta provides associated package  and commentary information related to
	// giving type.
	Meta Meta

	// resolver provides a means of the indexer to provide a custom resolving
	// function which will run internal logic to set giving values
	// appropriately during resolution of types.
	resolver ResolverFn
}

// Resolve takes the list of indexed packages to internal structures
// to resolve imported or internal types that they reference. This is
// used to ensure all package structures have direct link to parsed
// type.
func (p *Variable) Resolve(indexed map[string]*Package) error {
	if p.resolver != nil {
		if err := p.resolver(indexed); err != nil {
			return err
		}
	}
	return nil
}

// Constant holds related data related to information
// pertaining to declared constants.
type Constant struct {
	Ref
	Location

	// Path represents the giving full qualified package path name
	// and type name of type in format: PackagePath.TypeName.
	Path string

	// Name represents the name of giving interface.
	Name string

	// Exported is used to indicate if type is exported or not.
	Exported bool

	Basic *Base

	// Meta provides associated package  and commentary information related to
	// giving type.
	Meta Meta

	// resolver provides a means of the indexer to provide a custom resolving
	// function which will run internal logic to set giving values
	// appropriately during resolution of types.
	resolver ResolverFn
}

// Resolve takes the list of indexed packages to internal structures
// to resolve imported or internal types that they reference. This is
// used to ensure all package structures have direct link to parsed
// type.
func (p *Constant) Resolve(indexed map[string]*Package) error {
	if p.resolver != nil {
		if err := p.resolver(indexed); err != nil {
			return err
		}
	}
	return nil
}

// Base represents a golang base types which include
// strings, int types, floats, complex, etc, which are
// atomic indivisible types.
type Base struct {
	Ref
	Location

	// Path represents the giving full qualified package path name
	// and type name of type in format: PackagePath.TypeName.
	Path string

	// Name represents the name of giving type.
	Name string

	// Exported is used to indicate if type is exported or not.
	Exported bool

	// Type sets the value object/declared type.
	Type interface{}

	// Meta provides associated package  and commentary information related to
	// giving type.
	Meta Meta

	// resolver provides a means of the indexer to provide a custom resolving
	// function which will run internal logic to set giving values
	// appropriately during resolution of types.
	resolver ResolverFn
}

// Resolve takes the list of indexed packages to internal structures
// to resolve imported or internal types that they reference. This is
// used to ensure all package structures have direct link to parsed
// type.
func (p *Base) Resolve(indexed map[string]*Package) error {
	if p.resolver != nil {
		if err := p.resolver(indexed); err != nil {
			return err
		}
	}
	return nil
}

// Type defines a struct holding information about
// a defined custom type based on an existing type.
type Type struct {
	Ref
	Location
	Annotated

	// Path represents the giving full qualified package path name
	// and type name of type in format: PackagePath.TypeName.
	Path string

	// Name represents the name of giving interface.
	Name string

	// Exported is used to indicate if type is exported or not.
	Exported bool

	// IsFunction sets if giving type declaration is a function type declaration.
	IsFunction bool

	// Meta provides associated package  and commentary information related to
	// giving type.
	Meta Meta

	// Type sets the value object/declared type.
	Type interface{}

	// TypeName represents the type name of object type.
	TypeName string

	// resolver provides a means of the indexer to provide a custom resolving
	// function which will run internal logic to set giving values
	// appropriately during resolution of types.
	resolver ResolverFn
}

// Resolve takes the list of indexed packages to internal structures
// to resolve imported or internal types that they reference. This is
// used to ensure all package structures have direct link to parsed
// type.
func (p *Type) Resolve(indexed map[string]*Package) error {
	if p.resolver != nil {
		if err := p.resolver(indexed); err != nil {
			return err
		}
	}
	return nil
}

// Interface embodies necessary data related to declared
// interface types within a package.
type Interface struct {
	Ref
	Location
	Annotated

	// Meta provides associated package and commentary information related to
	// giving type.
	Meta Meta

	// Path represents the giving full qualified package path name
	// and type name of type in format: PackagePath.TypeName.
	Path string

	// Name represents the name of giving interface.
	Name string

	// Exported is used to indicate if type is exported or not.
	Exported bool

	// Composes contains all other interface types composed by
	// given interface type.
	Composes map[string]*Interface

	// Methods contains all method definitions/rules provided
	// as contract for interface implementors.
	Methods map[string]Function

	// resolver provides a means of the indexer to provide a custom resolving
	// function which will run internal logic to set giving values
	// appropriately during resolution of types.
	resolver ResolverFn
}

// Resolve takes the list of indexed packages to internal structures
// to resolve imported or internal types that they reference. This is
// used to ensure all package structures have direct link to parsed
// type.
func (p *Interface) Resolve(indexed map[string]*Package) error {
	if p.resolver != nil {
		if err := p.resolver(indexed); err != nil {
			return err
		}
	}
	for _, method := range p.Methods {
		if err := method.Resolve(indexed); err != nil {
			return err
		}
	}
	return nil
}

// Struct embodies necessary data related to declared
// struct types within a package.
type Struct struct {
	Ref
	Location
	Annotated

	// Path represents the giving full qualified package path name
	// and type name of type in format: PackagePath.TypeName.
	Path string

	// Name represents the name of giving interface.
	Name string

	// Exported is used to indicate if type is exported or not.
	Exported bool

	// Composes contains all interface types composed by
	// given struct type.
	Composes map[string]*Interface

	// Embeds contains all struct types composed by
	// given struct type.
	Embeds map[string]*Struct

	// Fields contains all fields and associated types declared
	// as members of struct.
	Fields map[string]Field

	// Methods contains all method definitions/rules provided
	// as contract for interface implementors.
	Methods map[string]Function

	// Meta provides associated package  and commentary information related to
	// giving type.
	Meta Meta

	// resolver provides a means of the indexer to provide a custom resolving
	// function which will run internal logic to set giving values
	// appropriately during resolution of types.
	resolver ResolverFn
}

// Resolve takes the list of indexed packages to internal structures
// to resolve imported or internal types that they reference. This is
// used to ensure all package structures have direct link to parsed
// type.
func (p *Struct) Resolve(indexed map[string]*Package) error {
	if p.resolver != nil {
		if err := p.resolver(indexed); err != nil {
			return err
		}
	}
	for _, field := range p.Fields {
		if err := field.Resolve(indexed); err != nil {
			return err
		}
	}
	for _, method := range p.Methods {
		if err := method.Resolve(indexed); err != nil {
			return err
		}
	}
	return nil
}

// Function embodies data related to declared
// package functions, struct methods, interface
// methods or function closures.
type Function struct {
	Ref
	Location
	Annotated

	// Body contains contents of Function containing
	// all statement declared within as it's body and
	// operation.
	Body []Expr

	// Path represents the giving full qualified package path name
	// and type name of type in format: PackagePath.TypeName.
	Path string

	// Name represents the name of giving interface.
	Name string

	// Exported is used to indicate if type is exported or not.
	Exported bool

	// IsMethod indicates if giving function is a method of a struct or a method
	// contract for an interface.
	IsMethod bool

	// IsAsync indicates whether function is called
	// asynchronously in goroutine.
	IsAsync bool

	// Owner sets the struct or interface which this function is attached
	// to has a method.
	Owner interface{}

	// OwnerName sets the name of the owner if struct or interface which
	// function is attached to else an empty string.
	OwnerName string

	// Arguments provides the argument list for giving function.
	Arguments []Parameter

	// Arguments provides the argument list for giving function.
	Returns []Parameter

	// Meta provides associated package  and commentary information related to
	// giving type.
	Meta Meta

	// resolver provides a means of the indexer to provide a custom resolving
	// function which will run internal logic to set giving values
	// appropriately during resolution of types.
	resolver ResolverFn
}

// Resolve takes the list of indexed packages to internal structures
// to resolve imported or internal types that they reference. This is
// used to ensure all package structures have direct link to parsed
// type.
func (p *Function) Resolve(indexed map[string]*Package) error {
	if p.resolver != nil {
		if err := p.resolver(indexed); err != nil {
			return err
		}
	}
	for _, param := range p.Returns {
		if err := param.Resolve(indexed); err != nil {
			return err
		}
	}
	for _, param := range p.Arguments {
		if err := param.Resolve(indexed); err != nil {
			return err
		}
	}
	for _, body := range p.Body {
		if err := body.Resolve(indexed); err != nil {
			return err
		}
	}
	return nil
}
