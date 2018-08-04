package runtime

// GetDefault returns the default value of the data-type type-name.
func GetDefault(t string) interface{} {
	return typeAndDefaultValue[t]
}

// typeAndDefaultValue contains the default values of all
// build in internal golang data types.
var typeAndDefaultValue = map[string]interface{}{
	"bool":       false,
	"byte":       0,
	"complex64":  0,
	"complex128": 0,
	"error":      nil,
	"float32":    0,
	"float64":    0,
	"int":        0,
	"int8":       0,
	"int16":      0,
	"int32":      0,
	"int64":      0,
	"rune":       0,
	"string":     "",
	"uint":       0,
	"uint8":      0,
	"uint16":     0,
	"uint32":     0,
	"uint64":     0,
	"uintptr":    0,
	"nil":        nil,
	"complex":    0,
	"real":       0,
	"func":       nil,
	"interface":  nil,
	"map":        nil,
	"struct":     nil,
	"chan":       nil,
}

// IsDefaultType returns true/false if giving string is
// a default builtin go data type.
func IsDefaultType(t string) bool {
	_, ok := defaultTypes[t]
	return ok
}

var defaultTypes = map[string]struct{}{
	"bool":       struct{}{},
	"byte":       struct{}{},
	"complex64":  struct{}{},
	"complex128": struct{}{},
	"error":      struct{}{},
	"float32":    struct{}{},
	"float64":    struct{}{},
	"int":        struct{}{},
	"int8":       struct{}{},
	"int16":      struct{}{},
	"int32":      struct{}{},
	"int64":      struct{}{},
	"rune":       struct{}{},
	"string":     struct{}{},
	"uint":       struct{}{},
	"uint8":      struct{}{},
	"uint16":     struct{}{},
	"uint32":     struct{}{},
	"uint64":     struct{}{},
	"uintptr":    struct{}{},
	"complex":    struct{}{},
	"real":       struct{}{},
	"func":       struct{}{},
	"interface":  struct{}{},
	"map":        struct{}{},
	"struct":     struct{}{},
	"chan":       struct{}{},
}

// IsBuiltIn returns true/false if giving t string is
// a special name or builtin name within Golang.
func IsBuiltIn(t string) bool {
	_, ok := builtinNames[t]
	return ok
}

// builtinNames contains a all builtin special names
// of golang.
var builtinNames = map[string]struct{}{
	"bool":        struct{}{},
	"byte":        struct{}{},
	"complex64":   struct{}{},
	"complex128":  struct{}{},
	"error":       struct{}{},
	"float32":     struct{}{},
	"float64":     struct{}{},
	"int":         struct{}{},
	"int8":        struct{}{},
	"int16":       struct{}{},
	"int32":       struct{}{},
	"int64":       struct{}{},
	"rune":        struct{}{},
	"string":      struct{}{},
	"uint":        struct{}{},
	"uint8":       struct{}{},
	"uint16":      struct{}{},
	"uint32":      struct{}{},
	"uint64":      struct{}{},
	"uintptr":     struct{}{},
	"true":        struct{}{},
	"false":       struct{}{},
	"iota":        struct{}{},
	"nil":         struct{}{},
	"append":      struct{}{},
	"cap":         struct{}{},
	"close":       struct{}{},
	"complex":     struct{}{},
	"copy":        struct{}{},
	"delete":      struct{}{},
	"imag":        struct{}{},
	"len":         struct{}{},
	"make":        struct{}{},
	"new":         struct{}{},
	"panic":       struct{}{},
	"print":       struct{}{},
	"println":     struct{}{},
	"real":        struct{}{},
	"recover":     struct{}{},
	"break":       struct{}{},
	"default":     struct{}{},
	"func":        struct{}{},
	"func()":      struct{}{},
	"interface":   struct{}{},
	"interface{}": struct{}{},
	"select":      struct{}{},
	"case":        struct{}{},
	"defer":       struct{}{},
	"go":          struct{}{},
	"map":         struct{}{},
	"struct":      struct{}{},
	"chan":        struct{}{},
	"else":        struct{}{},
	"goto":        struct{}{},
	"package":     struct{}{},
	"switch":      struct{}{},
	"const":       struct{}{},
	"fallthrough": struct{}{},
	"if":          struct{}{},
	"range":       struct{}{},
	"type":        struct{}{},
	"continue":    struct{}{},
	"for":         struct{}{},
	"import":      struct{}{},
	"return":      struct{}{},
	"var":         struct{}{},
}
