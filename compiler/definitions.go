package compiler

import "golang.org/x/tools/go/loader"

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
}

// DocText embodies a file level text not
// associated with any declaration exiting
// within a file.
type DocText struct {
	Location
	Text string
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
}

// PackageFile defines a giving package file with it's associated
// definitions, constructs and declarations. It provides the
// one-to-one relation of a parsed package.
type PackageFile struct {
	Name    string
	File    string
	Dir     string
	Docs    []Doc
	Imports []Import
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
	Functions  map[string]*Function
	Depends    map[string]*Package
	Files      map[string]*PackageFile
}

// Add adds giving declaration into package declaration
// types according to it's class.
func (p *Package) Add(declr interface{}) error {
	switch rdeclr := declr.(type) {
	case *Package:
		p.Depends[rdeclr.Name] = rdeclr
	case *Struct:
	case *Function:
	case *Map:
	case *Slice:
	case Variable:
		if rdeclr.Blank {
			p.Blanks = append(p.Blanks, rdeclr)
		} else {
			p.Variables = append(p.Variables, rdeclr)
		}
	case Constant:
		p.Constants = append(p.Constants, rdeclr)
	}
	return nil
}

// Resolve provides the list of indexed packages to internal structures
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

// Interface embodies necessary data related to declared
// interface types within a package.
type Interface struct {
	Location

	// Path represents the giving full qualified package path name
	// and type name of type in format: PackagePath.TypeName.
	Path string

	// Name represents the name of giving interface.
	Name string

	// Exported is used to indicate if type is exported or not.
	Exported bool

	// Methods contains all method definitions/rules provided
	// as contract for interface implementors.
	Methods []Function

	// Meta provides associated package and commentary information related to
	// giving type.
	Meta Meta

	// resolver provides a means of the indexer to provide a custom resolving
	// function which will run internal logic to set giving values
	// appropriately during resolution of types.
	resolver ResolverFn
}

// Resolve provides the list of indexed packages to internal structures
// to resolve imported or internal types that they reference. This is
// used to ensure all package structures have direct link to parsed
// type.
func (p *Interface) Resolve(indexed map[string]*Package) error {
	if p.resolver != nil {
		if err := p.resolver(indexed); err != nil {
			return err
		}
	}
	return nil
}

// Struct embodies necessary data related to declared
// struct types within a package.
type Struct struct {
	Location

	// Path represents the giving full qualified package path name
	// and type name of type in format: PackagePath.TypeName.
	Path string

	// Name represents the name of giving interface.
	Name string

	// Exported is used to indicate if type is exported or not.
	Exported bool

	// Methods contains all method definitions/rules provided
	// as contract for interface implementors.
	Methods []Function

	// Meta provides associated package  and commentary information related to
	// giving type.
	Meta Meta

	// resolver provides a means of the indexer to provide a custom resolving
	// function which will run internal logic to set giving values
	// appropriately during resolution of types.
	resolver ResolverFn
}

// Resolve provides the list of indexed packages to internal structures
// to resolve imported or internal types that they reference. This is
// used to ensure all package structures have direct link to parsed
// type.
func (p *Struct) Resolve(indexed map[string]*Package) error {
	if p.resolver != nil {
		if err := p.resolver(indexed); err != nil {
			return err
		}
	}
	return nil
}

// Function embodies data related to declared
// package functions, struct methods, interface
// methods or function closures.
type Function struct {
	Location

	// Path represents the giving full qualified package path name
	// and type name of type in format: PackagePath.TypeName.
	Path string

	// Name represents the name of giving interface.
	Name string

	// Exported is used to indicate if type is exported or not.
	Exported bool

	// IsAsync indicates whether function is called
	// asynchronously in goroutine.
	IsAsync bool

	// Struct sets the struct which function is attached
	// to has a method.
	Struct *Struct

	// Interface sets the interface which function definition
	// exists as part of.
	Interface *Interface

	// Arguments provides the argument list for giving function.
	Arguments []Parameter

	// Arguments provides the argument list for giving function.
	Returns []Parameter

	// Body contains contents of Function if not a Type declaration
	// or interface contract.
	Body []Expression

	// Meta provides associated package  and commentary information related to
	// giving type.
	Meta Meta

	// resolver provides a means of the indexer to provide a custom resolving
	// function which will run internal logic to set giving values
	// appropriately during resolution of types.
	resolver ResolverFn
}

// Resolve provides the list of indexed packages to internal structures
// to resolve imported or internal types that they reference. This is
// used to ensure all package structures have direct link to parsed
// type.
func (p *Function) Resolve(indexed map[string]*Package) error {
	if p.resolver != nil {
		if err := p.resolver(indexed); err != nil {
			return err
		}
	}
	return nil
}

// Field represents field types and names
// declared as part of a struct's properties.
type Field struct {
	Location

	// Path represents the giving full qualified package path name
	// and type name of type in format: PackagePath.TypeName.
	Path string

	// Name represents the name of giving interface.
	Name string

	// Meta provides associated package  and commentary information related to
	// giving type.
	Meta Meta

	Basic     *Base
	Struct    *Struct
	Func      *Function
	Type      *Type
	Interface *Interface
	Chan      *Channel
	Map       *Map
	Slice     *Slice

	// resolver provides a means of the indexer to provide a custom resolving
	// function which will run internal logic to set giving values
	// appropriately during resolution of types.
	resolver ResolverFn
}

// Resolve provides the list of indexed packages to internal structures
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

	// Name represents the name of giving interface.
	Name string

	// IsVariadic indicates if giving parameter is variadic.
	IsVariadic bool

	// Meta provides associated package  and commentary information related to
	// giving type.
	Meta Meta

	Basic     *Base
	Struct    *Struct
	Func      *Function
	Type      *Type
	Interface *Interface
	Chan      *Channel
	Map       *Map
	Slice     *Slice

	// resolver provides a means of the indexer to provide a custom resolving
	// function which will run internal logic to set giving values
	// appropriately during resolution of types.
	resolver ResolverFn
}

// Resolve provides the list of indexed packages to internal structures
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

// Resolve provides the list of indexed packages to internal structures
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
	Location

	// Path represents the giving full qualified package path name
	// and type name of type in format: PackagePath.TypeName.
	Path string

	// Name represents the name of giving interface.
	Name string

	// Exported is used to indicate if type is exported or not.
	Exported bool

	// Type defines the type associated with giving slice.
	Type interface{}

	// Meta provides associated package  and commentary information related to
	// giving type.
	Meta Meta

	// resolver provides a means of the indexer to provide a custom resolving
	// function which will run internal logic to set giving values
	// appropriately during resolution of types.
	resolver ResolverFn
}

// Resolve provides the list of indexed packages to internal structures
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

	Basic     *Base
	Chan      *Channel
	Struct    *Struct
	Type      *Type
	Interface *Interface
	Func      *Function
	Map       *Map
	Slice     *Slice

	// resolver provides a means of the indexer to provide a custom resolving
	// function which will run internal logic to set giving values
	// appropriately during resolution of types.
	resolver ResolverFn
}

// Resolve provides the list of indexed packages to internal structures
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
	Location

	Basic     *Base
	Chan      *Channel
	Struct    *Struct
	Type      *Type
	Interface *Interface
	Func      *Function
	Map       *Map
	Slice     *Slice

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

// Resolve provides the list of indexed packages to internal structures
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

// Resolve provides the list of indexed packages to internal structures
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
	// Path represents the giving full qualified package path name
	// and type name of type in format: PackagePath.TypeName.
	Path string

	// Name represents the name of giving interface.
	Name string

	// Exported is used to indicate if type is exported or not.
	Exported bool

	// Type sets the defined type name for giving base type.
	Type string

	// Meta provides associated package  and commentary information related to
	// giving type.
	Meta Meta

	// resolver provides a means of the indexer to provide a custom resolving
	// function which will run internal logic to set giving values
	// appropriately during resolution of types.
	resolver ResolverFn
}

// Resolve provides the list of indexed packages to internal structures
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
	Location

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

	Chan      *Channel
	Struct    *Struct
	Basic     *Base
	Type      *Type
	Interface *Interface
	Func      *Function
	Map       *Map
	Slice     *Slice

	// resolver provides a means of the indexer to provide a custom resolving
	// function which will run internal logic to set giving values
	// appropriately during resolution of types.
	resolver ResolverFn
}

// Resolve provides the list of indexed packages to internal structures
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

// ExpressionType defines a int type used to represent giving
// type of expressions such as assignment, multiplication,
// division, bracket closing, ...etc.
type ExpressionType int

// Expression provides a generic structure used to represent
// statements, special characters, symbols and operations generated
// from source code.
type Expression struct {
	Location

	Value    string
	Type     ExpressionType
	Children []Expression

	// Meta provides associated package  and commentary information related to
	// giving type.
	Meta Meta

	// resolver provides a means of the indexer to provide a custom resolving
	// function which will run internal logic to set giving values
	// appropriately during resolution of types.
	resolver ResolverFn
}

// Resolve provides the list of indexed packages to internal structures
// to resolve imported or internal types that they reference. This is
// used to ensure all package structures have direct link to parsed
// type.
func (p *Expression) Resolve(indexed map[string]*Package) error {
	if p.resolver != nil {
		if err := p.resolver(indexed); err != nil {
			return err
		}
	}
	return nil
}
