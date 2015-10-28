package main

import (
	"bytes"
	"go/ast"
	"go/format"
	"go/token"
	"strings"
	"text/template"

	"github.com/sbinet/go-clang"
)

type Function struct {
	Name    string
	CName   string
	Comment string

	Parameters []FunctionParameter
	ReturnType Type

	Receiver Receiver

	Member string
}

type FunctionParameter struct {
	Name  string
	CName string
	Type  Type
}

func handleFunctionCursor(cursor clang.Cursor) *Function {
	f := Function{
		CName:   cursor.Spelling(),
		Comment: cleanDoxygenComment(cursor.RawCommentText()),

		Parameters: []FunctionParameter{},
	}

	f.Name = strings.TrimPrefix(f.CName, "clang_")

	typ, err := getType(cursor.ResultType())
	if err != nil {
		panic(err)
	}
	f.ReturnType = typ

	numParam := uint(cursor.NumArguments())
	for i := uint(0); i < numParam; i++ {
		param := cursor.Argument(i)

		p := FunctionParameter{
			CName: param.DisplayName(),
		}

		p.Name = p.CName
		typ, err := getType(param.Type())
		if err != nil {
			panic(err)
		}
		p.Type = typ

		if p.Name == "" {
			p.Name = receiverName(p.Type.Name)
		}

		f.Parameters = append(f.Parameters, p)
	}

	return &f
}

func generateASTFunction(f *Function) string {
	astFunc := ast.FuncDecl{
		Name: &ast.Ident{
			Name: f.Name,
		},
		Type: &ast.FuncType{
			Results: &ast.FieldList{
				List: []*ast.Field{},
			},
		},
		Body: &ast.BlockStmt{},
	}

	addEmptyLine := func() {
		// TODO this should be done using something else than a fake statement.
		astFunc.Body.List = append(astFunc.Body.List, &ast.ExprStmt{
			X: &ast.CallExpr{
				Fun: &ast.Ident{
					Name: "REMOVE",
				},
			},
		})
	}

	// TODO maybe name the return arguments ... because of clang_getDiagnosticOption -> the normal return can be always just "o"?

	// TODO reenable this, see the comment at the bottom of the generate function for details
	// Add function comment
	/*if f.Comment != "" {
		astFunc.Doc = &ast.CommentGroup{
			List: []*ast.Comment{
				&ast.Comment{
					Text: f.Comment,
				},
			},
		}
	}*/

	// Add receiver to make function a method
	if f.Receiver.Name != "" {
		if len(f.Parameters) > 0 { // TODO maybe to not set the receiver at all? -> do this outside of the generate function?
			astFunc.Recv = &ast.FieldList{
				List: []*ast.Field{
					&ast.Field{
						Names: []*ast.Ident{
							&ast.Ident{
								Name: f.Receiver.Name,
							},
						},
						Type: &ast.Ident{
							Name: f.Receiver.Type.Name,
						},
					},
				},
			}
		}
	}

	// Basic call to the C function
	call := &ast.CallExpr{
		Fun: &ast.SelectorExpr{
			X: &ast.Ident{
				Name: "C",
			},
			Sel: &ast.Ident{
				Name: f.CName,
			},
		},
		Args: []ast.Expr{},
	}

	retur := &ast.ReturnStmt{
		Results: []ast.Expr{},
	}
	hasReturnArguments := false

	if len(f.Parameters) != 0 {
		if f.Receiver.Name != "" {
			f.Parameters[0].Name = f.Receiver.Name
		}

		astFunc.Type.Params = &ast.FieldList{
			List: []*ast.Field{},
		}

		// Add parameters to the function
		for i, p := range f.Parameters {
			if i == 0 && f.Receiver.Name != "" {
				continue
			}

			if p.Type.IsReturnArgument {
				hasReturnArguments = true

				// Add the return type to the function return arguments
				var retType string
				if p.Type.Name == "cxstring" {
					retType = "string"
				} else {
					retType = p.Type.Name
				}

				astFunc.Type.Results.List = append(astFunc.Type.Results.List, &ast.Field{
					Type: &ast.Ident{
						Name: retType,
					},
				})

				// Declare the return argument's variable
				var varType ast.Expr
				if p.Type.Primitive != "" {
					varType = &ast.SelectorExpr{
						X: &ast.Ident{
							Name: "C",
						},
						Sel: &ast.Ident{
							Name: p.Type.Primitive,
						},
					}
				} else {
					varType = &ast.Ident{
						Name: p.Type.Name,
					}
				}

				astFunc.Body.List = append(astFunc.Body.List, &ast.DeclStmt{
					Decl: &ast.GenDecl{
						Tok: token.VAR,
						Specs: []ast.Spec{
							&ast.ValueSpec{
								Names: []*ast.Ident{
									&ast.Ident{
										Name: p.Name,
									},
								},
								Type: varType,
							},
						},
					},
				})
				if p.Type.Name == "cxstring" {
					astFunc.Body.List = append(astFunc.Body.List, &ast.DeferStmt{
						Call: &ast.CallExpr{
							Fun: &ast.SelectorExpr{
								X: &ast.Ident{
									Name: p.Name,
								},
								Sel: &ast.Ident{
									Name: "Dispose",
								},
							},
						},
					})
				}

				// Add the return argument to the return statement
				if p.Type.Primitive != "" {
					retur.Results = append(retur.Results, &ast.CallExpr{
						Fun: &ast.Ident{
							Name: p.Type.Name,
						},
						Args: []ast.Expr{
							&ast.Ident{
								Name: p.Name,
							},
						},
					})
				} else {
					if p.Type.Name == "cxstring" {
						retur.Results = append(retur.Results, &ast.CallExpr{
							Fun: &ast.SelectorExpr{
								X: &ast.Ident{
									Name: p.Name,
								},
								Sel: &ast.Ident{
									Name: "String",
								},
							},
						})
					} else {
						retur.Results = append(retur.Results, &ast.Ident{
							Name: p.Name,
						})
					}
				}

				continue
			}

			astFunc.Type.Params.List = append(astFunc.Type.Params.List, &ast.Field{
				Names: []*ast.Ident{
					&ast.Ident{
						Name: p.Name,
					},
				},
				Type: &ast.Ident{
					Name: p.Type.Name,
				},
			})
		}

		if hasReturnArguments {
			addEmptyLine()
		}

		goToCTypeConversions := false

		// Add arguments to the C function call
		for _, p := range f.Parameters {
			var pf ast.Expr

			if p.Type.Primitive != "" {
				// Handle Go type to C type conversions
				if p.Type.CName == "const char *" {
					goToCTypeConversions = true

					astFunc.Body.List = append(astFunc.Body.List, &ast.AssignStmt{
						Lhs: []ast.Expr{
							&ast.Ident{
								Name: "c_" + p.Name,
							},
						},
						Tok: token.DEFINE,
						Rhs: []ast.Expr{
							&ast.CallExpr{
								Fun: &ast.SelectorExpr{
									X: &ast.Ident{
										Name: "C",
									},
									Sel: &ast.Ident{
										Name: "CString",
									},
								},
								Args: []ast.Expr{
									&ast.Ident{
										Name: p.Name,
									},
								},
							},
						},
					})
					astFunc.Body.List = append(astFunc.Body.List, &ast.DeferStmt{
						Call: &ast.CallExpr{
							Fun: &ast.SelectorExpr{
								X: &ast.Ident{
									Name: "C",
								},
								Sel: &ast.Ident{
									Name: "free",
								},
							},
							Args: []ast.Expr{
								&ast.CallExpr{
									Fun: &ast.SelectorExpr{
										X: &ast.Ident{
											Name: "unsafe",
										},
										Sel: &ast.Ident{
											Name: "Pointer",
										},
									},
									Args: []ast.Expr{
										&ast.Ident{
											Name: "c_" + p.Name,
										},
									},
								},
							},
						},
					})

					pf = &ast.Ident{
						Name: "c_" + p.Name,
					}
				} else if p.Type.Primitive == "cxstring" { // TODO try to get cxstring and "String" completely out of this function since it is just a struct which can be handled by the struct code
					pf = &ast.SelectorExpr{
						X: &ast.Ident{
							Name: p.Name,
						},
						Sel: &ast.Ident{
							Name: "c",
						},
					}
				} else {
					if p.Type.IsReturnArgument {
						// Return arguemnts already have a cast
						pf = &ast.Ident{
							Name: p.Name,
						}
					} else {
						pf = &ast.CallExpr{
							Fun: &ast.SelectorExpr{
								X: &ast.Ident{
									Name: "C",
								},
								Sel: &ast.Ident{
									Name: p.Type.Primitive,
								},
							},
							Args: []ast.Expr{
								&ast.Ident{
									Name: p.Name,
								},
							},
						}
					}
				}
			} else {
				pf = &ast.SelectorExpr{
					X: &ast.Ident{
						Name: p.Name,
					},
					Sel: &ast.Ident{
						Name: "c",
					},
				}
			}

			if p.Type.IsReturnArgument {
				pf = &ast.UnaryExpr{
					Op: token.AND,
					X:  pf,
				}
			}

			call.Args = append(call.Args, pf)
		}

		if goToCTypeConversions {
			addEmptyLine()
		}
	}

	// Check if we need to add a return
	if f.ReturnType.Name != "void" || hasReturnArguments {
		if f.ReturnType.Name != "void" {
			// Add the function return type
			astFunc.Type.Results.List = append(astFunc.Type.Results.List, &ast.Field{
				Type: &ast.Ident{
					Name: f.ReturnType.Name,
				},
			})
		}

		// Do we need to convert the return of the C function into a boolean?
		if f.ReturnType.Name == "bool" && f.ReturnType.Primitive != "" {
			// Do the C function call and save the result into the new variable "o"
			astFunc.Body.List = append(astFunc.Body.List, &ast.AssignStmt{
				Lhs: []ast.Expr{
					&ast.Ident{
						Name: "o",
					},
				},
				Tok: token.DEFINE,
				Rhs: []ast.Expr{
					call, // No cast needed
				},
			})

			addEmptyLine()

			// Check if o is not equal to zero and return the result
			retur.Results = append(retur.Results, &ast.BinaryExpr{
				X: &ast.Ident{
					Name: "o",
				},
				Op: token.NEQ,
				Y: &ast.CallExpr{
					Fun: &ast.SelectorExpr{
						X: &ast.Ident{
							Name: "C",
						},
						Sel: &ast.Ident{
							Name: f.ReturnType.Primitive,
						},
					},
					Args: []ast.Expr{
						&ast.BasicLit{
							Kind:  token.INT,
							Value: "0",
						},
					},
				},
			})
		} else if f.ReturnType.Name == "string" {
			// If this is a normal const char * C type there is not so much to do
			retur.Results = append(retur.Results, &ast.CallExpr{
				Fun: &ast.SelectorExpr{
					X: &ast.Ident{
						Name: "C",
					},
					Sel: &ast.Ident{
						Name: "GoString",
					},
				},
				Args: []ast.Expr{
					call,
				},
			})
		} else if f.ReturnType.Name == "cxstring" {
			// Do the C function call and save the result into the new variable "o" while transforming it into a cxstring
			astFunc.Body.List = append(astFunc.Body.List, &ast.AssignStmt{
				Lhs: []ast.Expr{
					&ast.Ident{
						Name: "o",
					},
				},
				Tok: token.DEFINE,
				Rhs: []ast.Expr{
					&ast.CompositeLit{
						Type: &ast.Ident{
							Name: "cxstring",
						},
						Elts: []ast.Expr{
							call,
						},
					},
				},
			})
			astFunc.Body.List = append(astFunc.Body.List, &ast.DeferStmt{
				Call: &ast.CallExpr{
					Fun: &ast.SelectorExpr{
						X: &ast.Ident{
							Name: "o",
						},
						Sel: &ast.Ident{
							Name: "Dispose",
						},
					},
				},
			})

			addEmptyLine()

			// Call the String method on the cxstring instance
			retur.Results = append(retur.Results, &ast.CallExpr{
				Fun: &ast.SelectorExpr{
					X: &ast.Ident{
						Name: "o",
					},
					Sel: &ast.Ident{
						Name: "String",
					},
				},
			})

			// Change the return type to "string"
			astFunc.Type.Results.List[len(astFunc.Type.Results.List)-1] = &ast.Field{
				Type: &ast.Ident{
					Name: "string",
				},
			}
		} else if f.ReturnType.Name == "time.Time" {
			retur.Results = append(retur.Results, &ast.CallExpr{
				Fun: &ast.SelectorExpr{
					X: &ast.Ident{
						Name: "time",
					},
					Sel: &ast.Ident{
						Name: "Unix",
					},
				},
				Args: []ast.Expr{
					&ast.CallExpr{
						Fun: &ast.Ident{
							Name: "int64",
						},
						Args: []ast.Expr{
							call,
						},
					},
					&ast.BasicLit{
						Kind:  token.INT,
						Value: "0",
					},
				},
			})
		} else if f.ReturnType.Name == "void" {
			// Handle the case where the C function has no return argument but parameters that are return arguments

			// Do the C function call
			astFunc.Body.List = append(astFunc.Body.List, &ast.ExprStmt{
				X: call,
			})

			addEmptyLine()
		} else {
			var convCall ast.Expr

			// Structs are literals, everything else is a cast
			if f.ReturnType.Primitive == "" {
				convCall = &ast.CompositeLit{
					Type: &ast.Ident{
						Name: f.ReturnType.Name,
					},
					Elts: []ast.Expr{
						call,
					},
				}
			} else {
				convCall = &ast.CallExpr{
					Fun: &ast.Ident{
						Name: f.ReturnType.Name,
					},
					Args: []ast.Expr{
						call,
					},
				}
			}

			if hasReturnArguments {
				// Do the C function call and save the result into the new variable "o"
				astFunc.Body.List = append(astFunc.Body.List, &ast.AssignStmt{
					Lhs: []ast.Expr{
						&ast.Ident{
							Name: "o",
						},
					},
					Tok: token.DEFINE,
					Rhs: []ast.Expr{
						convCall,
					},
				})

				addEmptyLine()

				// Add the C function call result to the return statement
				retur.Results = append(retur.Results, &ast.Ident{
					Name: "o",
				})
			} else {
				retur.Results = append(retur.Results, convCall)
			}
		}

		// Add the return statement
		astFunc.Body.List = append(astFunc.Body.List, retur)
	} else {
		// No return needed, just add the C function call
		astFunc.Body.List = append(astFunc.Body.List, &ast.ExprStmt{
			X: call,
		})
	}

	var b bytes.Buffer
	err := format.Node(&b, token.NewFileSet(), []ast.Decl{&astFunc})
	if err != nil {
		panic(err)
	}

	sss := b.String()

	// TODO hack to make new lines...
	sss = strings.Replace(sss, "REMOVE()", "", -1)

	// TODO find out how to position the comment correctly and do this using the AST
	if f.Comment != "" {
		sss = f.Comment + "\n" + sss
	}

	return sss
}

var templateGenerateStructMemberGetter = template.Must(template.New("go-clang-generate-function-getter").Parse(`{{$.Comment}}
func ({{$.Receiver.Name}} {{$.Receiver.Type.Name}}) {{$.Name}}() {{if ge $.ReturnType.PointerLevel 1}}*{{end}}{{$.ReturnType.Name}} {
	value := {{if eq $.ReturnType.Name "bool"}}{{$.Receiver.Name}}.c.{{$.Member}}{{else}}{{$.ReturnType.Name}}{{if $.ReturnType.IsPrimitive}}({{if ge $.ReturnType.PointerLevel 1}}*{{end}}{{$.Receiver.Name}}.c.{{$.Member}}){{else}}{{"{"}}{{if ge $.ReturnType.PointerLevel 1}}*{{end}}{{$.Receiver.Name}}.c.{{$.Member}}{{"}"}}{{end}}{{end}}
	return {{if eq $.ReturnType.Name "bool"}}value != C.int(0){{else}}{{if ge $.ReturnType.PointerLevel 1}}&{{end}}value{{end}}
}
`))

func generateFunctionStructMemberGetter(f *Function) string {
	var b bytes.Buffer
	if err := templateGenerateStructMemberGetter.Execute(&b, f); err != nil {
		panic(err)
	}

	return b.String()
}

type FunctionSliceReturn struct {
	Function

	SizeMember string

	CElementType    string
	ElementType     string
	IsPrimitive     bool
	ArrayDimensions int
	ArraySize       int64
}

var templateGenerateReturnSlice = template.Must(template.New("go-clang-generate-slice").Parse(`{{$.Comment}}
func ({{$.Receiver.Name}} {{$.Receiver.Type.Name}}) {{$.Name}}() []{{if eq $.ArrayDimensions 2 }}*{{end}}{{$.ElementType}} {
	sc := []{{if eq $.ArrayDimensions 2 }}*{{end}}{{$.ElementType}}{} 

	length := {{if ne $.ArraySize -1}}{{$.ArraySize}}{{else}}int({{$.Receiver.Name}}.c.{{$.SizeMember}}){{end}}
	goslice := (*[1 << 30]{{if or (eq $.ArrayDimensions 2) (eq $.ElementType "unsafe.Pointer")}}*{{end}}C.{{$.CElementType}})(unsafe.Pointer(&{{$.Receiver.Name}}.c.{{$.Member}}))[:length:length]

	for is := 0; is < length; is++ {
		sc = append(sc, {{if eq $.ArrayDimensions 2}}&{{end}}{{$.ElementType}}{{if $.IsPrimitive}}({{if eq $.ArrayDimensions 2}}*{{end}}goslice[is]){{else}}{{"{"}}{{if eq $.ArrayDimensions 2}}*{{end}}goslice[is]{{"}"}}{{end}})
	}

	return sc
}
`))

func generateFunctionSliceReturn(f *FunctionSliceReturn) string {
	var b bytes.Buffer
	if err := templateGenerateReturnSlice.Execute(&b, f); err != nil {
		panic(err)
	}

	return b.String()

}

func generateFunction(name, cname, comment, member string, typ Type) *Function {
	receiverType := trimClangPrefix(cname)
	receiverName := receiverName(receiverType)
	functionName := upperFirstCharacter(name)

	if typ.IsPrimitive {
		typ.Primitive = typ.Name
	}
	if (strings.HasPrefix(name, "has") || strings.HasPrefix(name, "is")) && typ.Name == GoInt32 {
		typ.Name = GoBool
	}

	f := &Function{
		Name:    functionName,
		CName:   cname,
		Comment: comment,

		Parameters: []FunctionParameter{},

		ReturnType: typ,
		Receiver: Receiver{
			Name: receiverName,
			Type: Type{
				Name: receiverType,
			},
		},

		Member: member,
	}

	return f
}
