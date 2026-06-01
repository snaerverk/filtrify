package roqql

import (
	"context"
	"reflect"
	"strings"
	"testing"

	astv1 "github.com/roderm/filtrify/pkg/generated/filtrify/ast/v1"
)

func TestParser_Parse(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		wantErr bool
		want    *astv1.Expr
	}{
		{
			name:    "Simple Comparison",
			input:   `status = "active"`,
			wantErr: false,
			want: &astv1.Expr{
				Condition: &astv1.Expr_Comp{
					Comp: &astv1.Comparison{
						Field:    "status",
						Operator: "eq",
						Value: &astv1.Value{
							Value: &astv1.Value_String_{
								String_: "active",
							},
						},
					},
				},
			},
		},
		{
			name:    "AND Comparison",
			input:   `status = "active" AND priority = 1`,
			wantErr: false,
			want: &astv1.Expr{
				Condition: &astv1.Expr_And{
					And: &astv1.AndExpr{
						Conditions: []*astv1.Expr{
							{
								Condition: &astv1.Expr_Comp{
									Comp: &astv1.Comparison{
										Field:    "status",
										Operator: "eq",
										Value: &astv1.Value{
											Value: &astv1.Value_String_{
												String_: "active",
											},
										},
									},
								},
							},
							{
								Condition: &astv1.Expr_Comp{
									Comp: &astv1.Comparison{
										Field:    "priority",
										Operator: "eq",
										Value: &astv1.Value{
											Value: &astv1.Value_Integer{
												Integer: 1,
											},
										},
									},
								},
							},
						},
					},
				},
			},
		},
		{
			name:    "OR Comparison",
			input:   `status = "active" OR status = "pending"`,
			wantErr: false,
			want: &astv1.Expr{
				Condition: &astv1.Expr_Or{
					Or: &astv1.OrExpr{
						Conditions: []*astv1.Expr{
							{
								Condition: &astv1.Expr_Comp{
									Comp: &astv1.Comparison{
										Field:    "status",
										Operator: "eq",
										Value: &astv1.Value{
											Value: &astv1.Value_String_{
												String_: "active",
											},
										},
									},
								},
							},
							{
								Condition: &astv1.Expr_Comp{
									Comp: &astv1.Comparison{
										Field:    "status",
										Operator: "eq",
										Value: &astv1.Value{
											Value: &astv1.Value_String_{
												String_: "pending",
											},
										},
									},
								},
							},
						},
					},
				},
			},
		},
		{
			name:    "Complex AND OR",
			input:   `(status = "active" OR status = "pending") AND priority = 1`,
			wantErr: false,
			want: &astv1.Expr{
				Condition: &astv1.Expr_And{
					And: &astv1.AndExpr{
						Conditions: []*astv1.Expr{
							{
								Condition: &astv1.Expr_Or{
									Or: &astv1.OrExpr{
										Conditions: []*astv1.Expr{
											{
												Condition: &astv1.Expr_Comp{
													Comp: &astv1.Comparison{
														Field:    "status",
														Operator: "eq",
														Value: &astv1.Value{
															Value: &astv1.Value_String_{
																String_: "active",
															},
														},
													},
												},
											},
											{
												Condition: &astv1.Expr_Comp{
													Comp: &astv1.Comparison{
														Field:    "status",
														Operator: "eq",
														Value: &astv1.Value{
															Value: &astv1.Value_String_{
																String_: "pending",
															},
														},
													},
												},
											},
										},
									},
								},
							},
							{
								Condition: &astv1.Expr_Comp{
									Comp: &astv1.Comparison{
										Field:    "priority",
										Operator: "eq",
										Value: &astv1.Value{
											Value: &astv1.Value_Integer{
												Integer: 1,
											},
										},
									},
								},
							},
						},
					},
				},
			},
		},
		{
			name:    "NOT Expression",
			input:   `NOT status = "inactive"`,
			wantErr: false,
			want: &astv1.Expr{
				Condition: &astv1.Expr_Not{
					Not: &astv1.NotExpr{
						Conditions: &astv1.Expr{
							Condition: &astv1.Expr_Comp{
								Comp: &astv1.Comparison{
									Field:    "status",
									Operator: "eq",
									Value: &astv1.Value{
										Value: &astv1.Value_String_{
											String_: "inactive",
										},
									},
								},
							},
						},
					},
				},
			},
		},
		{
			name:    "Has Expression",
			input:   `has(user, status = "active")`,
			wantErr: false,
			want: &astv1.Expr{
				Condition: &astv1.Expr_Has{
					Has: &astv1.HasChildren{
						Entity: "user",
						With: &astv1.Expr{
							Condition: &astv1.Expr_Comp{
								Comp: &astv1.Comparison{
									Field:    "status",
									Operator: "eq",
									Value: &astv1.Value{
										Value: &astv1.Value_String_{
											String_: "active",
										},
									},
								},
							},
						},
					},
				},
			},
		},
		{
			name:    "Nested Has",
			input:   `has(user, has(company, name = "Google"))`,
			wantErr: false,
			want: &astv1.Expr{
				Condition: &astv1.Expr_Has{
					Has: &astv1.HasChildren{
						Entity: "user",
						With: &astv1.Expr{
							Condition: &astv1.Expr_Has{
								Has: &astv1.HasChildren{
									Entity: "company",
									With: &astv1.Expr{
										Condition: &astv1.Expr_Comp{
											Comp: &astv1.Comparison{
												Field:    "name",
												Operator: "eq",
												Value: &astv1.Value{
													Value: &astv1.Value_String_{
														String_: "Google",
													},
												},
											},
										},
									},
								},
							},
						},
					},
				},
			},
		},
		{
			name:    "Boolean Value",
			input:   `is_verified = true`,
			wantErr: false,
			want: &astv1.Expr{
				Condition: &astv1.Expr_Comp{
					Comp: &astv1.Comparison{
						Field:    "is_verified",
						Operator: "eq",
						Value: &astv1.Value{
							Value: &astv1.Value_Bool{
								Bool: true,
							},
						},
					},
				},
			},
		},
		{
			name:    "Number Value",
			input:   `count >= 10`,
			wantErr: false,
			want: &astv1.Expr{
				Condition: &astv1.Expr_Comp{
					Comp: &astv1.Comparison{
						Field:    "count",
						Operator: "gte",
						Value: &astv1.Value{
							Value: &astv1.Value_Integer{
								Integer: 10,
							},
						},
					},
				},
			},
		},
		{
			name:    "Contains Expression",
			input:   `contains("firstname", "peter")`,
			wantErr: false,
			want: &astv1.Expr{
				Condition: &astv1.Expr_Comp{
					Comp: &astv1.Comparison{
						Field:    "firstname",
						Operator: "contains",
						Value: &astv1.Value{
							Value: &astv1.Value_String_{
								String_: "peter",
							},
						},
					},
				},
			},
		},
		{
			name:    "In Expression",
			input:   `in("firstname", ['peter','aline' , "klaus"])`,
			wantErr: false,
			want: &astv1.Expr{
				Condition: &astv1.Expr_Comp{
					Comp: &astv1.Comparison{
						Field:    "firstname",
						Operator: "in",
						Value: &astv1.Value{
							Value: &astv1.Value_List{
								List: &astv1.List{
									Values: []*astv1.Value{
										{
											Value: &astv1.Value_String_{
												String_: "peter",
											},
										},
										{
											Value: &astv1.Value_String_{
												String_: "aline",
											},
										},
										{
											Value: &astv1.Value_String_{
												String_: "klaus",
											},
										},
									},
								},
							},
						},
					},
				},
			},
		},
		{
			name:    "Invalid Syntax",
			input:   `status = `,
			wantErr: true,
		},
		{
			name:    "Incomplete Expression",
			input:   `(status = "active"`,
			wantErr: true,
		},
	}

	p := &Parser{}
	ctx := context.Background()

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := strings.NewReader(tt.input)
			got, err := p.Parse(ctx, r)
			if (err != nil) != tt.wantErr {
				t.Errorf("Parser.Parse() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !tt.wantErr && tt.want != nil && !reflect.DeepEqual(got, tt.want) {
				t.Errorf("Parser.Parse() \ngot =>\t %+v \nwant =>\t %+v", got, tt.want)
			}
		})
	}
}
