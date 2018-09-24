package compiler

var (
	// AnyType defines giving type as interface{}.
	AnyType = &Type{
		Name: "interface{}",
	}

	// ErrorType defines base package-wide error type.
	ErrorType = &Interface{
		Name: "error",
		Methods: map[string]*Function{
			"error.Error": {
				Name: "Error",
				Returns: []Parameter{
					{
						Name: "string",
						Type: BaseFor("string"),
					},
				},
			},
		},
	}
)
