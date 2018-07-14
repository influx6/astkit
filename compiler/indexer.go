package compiler

import (
	"gitlab.com/gokit/astkit/compiler/definitions"
	"golang.org/x/tools/go/loader"
)

// Index defnes a package level indexer which handles processing
//
var Indexer AstIndexer

type AstIndexer struct{}

// Index takes provided loader.Program returning a definitions.Package containing
// parsed structures, types and declarations of all packages.
func (i AstIndexer) Index(p *loader.Program) (definitions.Package, error) {
	var pkg definitions.Package

	return pkg, nil
}

// indexPackage takes provided loader.Program returning a definitions.Package containing
// parsed structures, types and declarations of all packages.
func (i AstIndexer) indexPackage(p *loader.PackageInfo) (definitions.Package, error) {
	var pkg definitions.Package

	return pkg, nil
}
