AstKit
---------
AstKit is a library built to help reduce the surface area required to work with the generated AST structures from the internal
Golang runtime [Ast](https://golang.org/pkg/go/ast), [Tokens](https://golang.org/pkg/go/tokens) and [Types](https://golang.org/pkg/go/types)
packages.

AstKit is most suitable for use as a means of generating meta data that can be used to create tools that better understand
existing sources, or as input for futher code generation needs. It's geared towards providing a clear, simpler
structures for the different Golang structures provided as of Go 1.11.

*An important gotcha with AstKit is that generated structures which could not be processed or connected
will be represented by a unknown expression `UnknownExpr` type, which might be a gotcha. This may heavily occur
especially during processing of function bodies and variables or types which could not be located.*

## Install

```bash
go get -u github.com/gokit/astkit
```

## Usage

```go
ctx := context.Background()
sudo, imported, err := astkit.Transform(ctx, "github.com/gokit/sudo")
```