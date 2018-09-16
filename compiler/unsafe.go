package compiler

var (
	unsafeAbitraryType = &Type{
		Exported: true,
		Name:     "ArbitraryType",
		Points:   BaseFor("int"),
		Meta:     Meta{Name: "unsafe", Path: "unsafe"},
		Pathway:  Pathway{Path: "unsafe.ArbitraryType"},
		Location: Location{
			End:       15,
			Line:      15,
			Begin:     15,
			Column:    0,
			Length:    21,
			LineEnd:   15,
			ColumnEnd: 21,
		},
		Commentaries: Commentaries{
			Doc: Doc{
				Text: "// ArbitraryType is here for the purposes of documentation only and is not actually\n// part of the unsafe package. It represents the type of an arbitrary Go expression.",
				Location: &Location{
					End:       14,
					Line:      13,
					Begin:     13,
					Column:    0,
					Length:    300,
					LineEnd:   14,
					ColumnEnd: 21,
				},
			},
		},
	}

	unsafePointer = &Type{
		Exported: true,
		Name:     "Pointer",
		Points:   unsafeAbitraryType,
		Meta:     Meta{Name: "unsafe", Path: "unsafe"},
		Pathway:  Pathway{Path: "unsafe.Pointer"},
		Location: Location{
			End:       15,
			Line:      15,
			Begin:     15,
			Column:    0,
			Length:    21,
			LineEnd:   15,
			ColumnEnd: 21,
		},
		Commentaries: Commentaries{
			Doc: Doc{
				Text: "// ArbitraryType is here for the purposes of documentation only and is not actually\n// part of the unsafe package. It represents the type of an arbitrary Go expression.",
				Location: &Location{
					End:       14,
					Line:      13,
					Begin:     13,
					Column:    0,
					Length:    300,
					LineEnd:   14,
					ColumnEnd: 21,
				},
			},
		},
	}

	unsafeAlignof = &Function{
		Exported: true,
		Name:     "Alignof",
		Meta:     Meta{Name: "unsafe", Path: "unsafe"},
		Arguments: []Parameter{
			{
				Name: "x",
				Type: unsafeAbitraryType,
			},
		},
		Returns: []Parameter{
			{
				Name: "",
				Type: BaseFor("uintptr"),
			},
		},
		Pathway: Pathway{Path: "unsafe.Sizeof"},
		Location: Location{
			End:       15,
			Line:      15,
			Begin:     15,
			Column:    0,
			Length:    21,
			LineEnd:   15,
			ColumnEnd: 21,
		},
		Commentaries: Commentaries{
			Doc: Doc{
				Text: `
// Alignof takes an expression x of any type and returns the required alignment
// of a hypothetical variable v as if v was declared via var v = x.
// It is the largest value m such that the address of v is always zero mod m.
// It is the same as the value returned by reflect.TypeOf(x).Align().
// As a special case, if a variable s is of struct type and f is a field
// within that struct, then Alignof(s.f) will return the required alignment
// of a field of that type within a struct. This case is the same as the
// value returned by reflect.TypeOf(s.f).FieldAlign().
				`,
				Location: &Location{
					End:       14,
					Line:      13,
					Begin:     13,
					Column:    0,
					Length:    300,
					LineEnd:   14,
					ColumnEnd: 21,
				},
			},
		},
	}

	unsafeOffset = &Function{
		Exported: true,
		Name:     "Offset",
		Meta:     Meta{Name: "unsafe", Path: "unsafe"},
		Arguments: []Parameter{
			{
				Name: "x",
				Type: unsafeAbitraryType,
			},
		},
		Returns: []Parameter{
			{
				Name: "",
				Type: BaseFor("uintptr"),
			},
		},
		Pathway: Pathway{Path: "unsafe.Sizeof"},
		Location: Location{
			End:     15,
			Line:    196,
			Begin:   15,
			Length:  50,
			LineEnd: 196,
		},
		Commentaries: Commentaries{
			Doc: Doc{
				Text: `
// Offsetof returns the offset within the struct of the field represented by x,
// which must be of the form structValue.field. In other words, it returns the
// number of bytes between the start of the struct and the start of the field.
				`,
				Location: &Location{
					End:     14,
					Line:    13,
					Begin:   13,
					Length:  300,
					LineEnd: 14,
				},
			},
		},
	}

	unsafeSizeOf = &Function{
		Exported: true,
		Name:     "Sizeof",
		Meta:     Meta{Name: "unsafe", Path: "unsafe"},
		Arguments: []Parameter{
			{
				Name: "x",
				Type: unsafeAbitraryType,
			},
		},
		Returns: []Parameter{
			{
				Name: "",
				Type: BaseFor("uintptr"),
			},
		},
		Pathway: Pathway{Path: "unsafe.Sizeof"},
		Location: Location{
			End:       15,
			Line:      15,
			Begin:     15,
			Column:    0,
			Length:    21,
			LineEnd:   15,
			ColumnEnd: 21,
		},
		Commentaries: Commentaries{
			Doc: Doc{
				Text: "// Sizeof takes an expression x of any type and returns the size in bytes\n// of a hypothetical variable v as " +
					"if v was declared via var v = x.\n// The size does not include any memory possibly referenced by x.\n" +
					"// For instance, if x is a slice, Sizeof returns the size of the slice\n// descriptor, " +
					"not the size of the memory referenced by the slice.",
				Location: &Location{
					End:       14,
					Line:      13,
					Begin:     13,
					Column:    0,
					Length:    300,
					LineEnd:   14,
					ColumnEnd: 21,
				},
			},
		},
	}

	unsafePackage = &Package{
		Name: "unsafe",
		Functions: map[string]*Function{
			"unsafe.Sizeof":   unsafeSizeOf,
			"unsafe.Offsetof": unsafeOffset,
			"unsafe.Alignof":  unsafeAlignof,
		},
		Types: map[string]*Type{
			"unsafe.Pointer":       unsafePointer,
			"unsafe.ArbitraryType": unsafeAbitraryType,
		},
	}
)
