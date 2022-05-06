package logicalplan

import (
	"encoding/json"
	"fmt"
	"reflect"

	"github.com/apache/arrow/go/v8/arrow"
	"github.com/apache/arrow/go/v8/arrow/scalar"
	"github.com/segmentio/parquet-go"

	"github.com/polarsignals/arcticdb/dynparquet"
)

type Operator int

const (
	EqOp Operator = iota
	NotEqOp
	GTOp
	GTEOp
	LTOp
	LTEOp
	RegExpOp
	NotRegExpOp
	AndOp
)

func (o Operator) String() string {
	switch o {
	case EqOp:
		return "=="
	case NotEqOp:
		return "!="
	case GTOp:
		return ">"
	case GTEOp:
		return ">="
	case LTOp:
		return "<"
	case LTEOp:
		return "<="
	case RegExpOp:
		return "=~"
	case NotRegExpOp:
		return "!~"
	case AndOp:
		return "&&"
	default:
		panic("unknown operator")
	}
}

type BinaryExpr struct {
	Left  Expr
	Op    Operator
	Right Expr
}

func (e *BinaryExpr) MarshalJSON() ([]byte, error) {
	type binaryExprJSON struct {
		LeftType  string
		Left      Expr
		RightType string
		Right     Expr
		Op        Operator
	}
	return json.Marshal(binaryExprJSON{
		LeftType:  reflect.TypeOf(e.Left).String(),
		Left:      e.Left,
		Op:        e.Op,
		RightType: reflect.TypeOf(e.Right).String(),
		Right:     e.Right,
	})
}

func (e *BinaryExpr) UnmarshalJSON(data []byte) error {
	type binaryExprJSON struct {
		LeftType  string
		Left      json.RawMessage
		RightType string
		Right     json.RawMessage
		Op        Operator
	}
	var bej binaryExprJSON
	err := json.Unmarshal(data, &bej)
	if err != nil {
		return err
	}

	e.Op = bej.Op

	switch bej.LeftType {
	case "*logicalplan.Column":
		var c Column
		err := json.Unmarshal(bej.Left, &c)
		if err != nil {
			return err
		}
		e.Left = &c
	case "*logicalplan.BinaryExpr":
		var be BinaryExpr
		err := json.Unmarshal(bej.Left, &be)
		if err != nil {
			return err
		}
		e.Left = &be
	default:
		panic(fmt.Sprintf("BinaryExpr unmarshalling for %s hasn't been implemented", bej.LeftType))
	}
	switch bej.RightType {
	case "*logicalplan.LiteralExpr":
		var literal LiteralExpr
		err := json.Unmarshal(bej.Right, &literal)
		if err != nil {
			return err
		}
		e.Right = &literal
	case "*logicalplan.BinaryExpr":
		var be BinaryExpr
		err := json.Unmarshal(bej.Right, &be)
		if err != nil {
			return err
		}
		e.Right = &be
	default:
		panic(fmt.Sprintf("BinaryExpr unmarshalling for %s hasn't been implemented", bej.RightType))
	}
	return nil
}

func (e BinaryExpr) Accept(visitor Visitor) bool {
	continu := visitor.PreVisit(&e)
	if !continu {
		return false
	}

	continu = e.Left.Accept(visitor)
	if !continu {
		return false
	}

	continu = e.Right.Accept(visitor)
	if !continu {
		return false
	}

	return visitor.PostVisit(&e)
}

func (e BinaryExpr) DataType(_ *dynparquet.Schema) arrow.DataType {
	return &arrow.BooleanType{}
}

func (e BinaryExpr) Name() string {
	return e.Left.Name() + " " + e.Op.String() + " " + e.Right.Name()
}

func (e BinaryExpr) ColumnsUsed() []ColumnMatcher {
	return append(e.Left.ColumnsUsed(), e.Right.ColumnsUsed()...)
}

func (e BinaryExpr) Matcher() ColumnMatcher {
	return StaticColumnMatcher{ColumnName: e.Name()}
}

func (e BinaryExpr) Alias(alias string) AliasExpr {
	return AliasExpr{Expr: &e, Alias: alias}
}

type Column struct {
	ColumnName string
}

func (c *Column) MarshalJSON() ([]byte, error) {
	type columnJSON struct {
		Expr       string
		ColumnName string
	}
	return json.Marshal(columnJSON{
		Expr:       reflect.TypeOf(c.ColumnName).String(),
		ColumnName: c.ColumnName,
	})
}

func (c *Column) UnmarshalJSON(data []byte) error {
	type columnJSON struct {
		Expr       string
		ColumnName string
	}
	var cj columnJSON
	err := json.Unmarshal(data, &cj)
	if err != nil {
		return err
	}
	c.ColumnName = cj.ColumnName
	return nil
}

func (c *Column) Accept(visitor Visitor) bool {
	continu := visitor.PreVisit(c)
	if !continu {
		return false
	}

	return visitor.PostVisit(c)
}

func (c *Column) Name() string {
	return c.ColumnName
}

func (c *Column) DataType(s *dynparquet.Schema) arrow.DataType {
	colDef, found := s.ColumnByName(c.ColumnName)
	if !found {
		panic("column not found")
	}

	return ParquetNodeToType(colDef.StorageLayout)
}

func (c *Column) Alias(alias string) AliasExpr {
	return AliasExpr{Expr: c, Alias: alias}
}

// ParquetNodeToType converts a parquet node to an arrow type and a function to
// create a value writer.
func ParquetNodeToType(n parquet.Node) arrow.DataType {
	t := n.Type()
	lt := t.LogicalType()
	switch {
	case lt.UUID != nil:
		return &arrow.FixedSizeBinaryType{
			ByteWidth: 16,
		}
	case lt.UTF8 != nil:
		return &arrow.StringType{}
	case lt.Integer != nil:
		switch lt.Integer.BitWidth {
		case 64:
			if lt.Integer.IsSigned {
				return &arrow.Int64Type{}
			}
			return &arrow.Uint64Type{}
		default:
			panic("unsupported int bit width")
		}
	default:
		panic("unsupported type for parquet to arrow conversion")
	}
}

func (c *Column) ColumnsUsed() []ColumnMatcher {
	return []ColumnMatcher{c.Matcher()}
}

func (c *Column) Matcher() ColumnMatcher {
	return StaticColumnMatcher{ColumnName: c.ColumnName}
}

func (c *Column) Eq(e Expr) *BinaryExpr {
	return &BinaryExpr{
		Left:  c,
		Op:    EqOp,
		Right: e,
	}
}

func (c *Column) NotEq(e Expr) *BinaryExpr {
	return &BinaryExpr{
		Left:  c,
		Op:    NotEqOp,
		Right: e,
	}
}

func (c *Column) GT(e Expr) *BinaryExpr {
	return &BinaryExpr{
		Left:  c,
		Op:    GTOp,
		Right: e,
	}
}

func (c *Column) GTE(e Expr) *BinaryExpr {
	return &BinaryExpr{
		Left:  c,
		Op:    GTEOp,
		Right: e,
	}
}

func (c *Column) LT(e Expr) *BinaryExpr {
	return &BinaryExpr{
		Left:  c,
		Op:    LTOp,
		Right: e,
	}
}

func (c *Column) LTE(e Expr) *BinaryExpr {
	return &BinaryExpr{
		Left:  c,
		Op:    LTEOp,
		Right: e,
	}
}

func (c *Column) RegexMatch(pattern string) *BinaryExpr {
	return &BinaryExpr{
		Left:  c,
		Op:    RegExpOp,
		Right: Literal(pattern),
	}
}

func (c *Column) RegexNotMatch(pattern string) *BinaryExpr {
	return &BinaryExpr{
		Left:  c,
		Op:    NotRegExpOp,
		Right: Literal(pattern),
	}
}

func Col(name string) *Column {
	return &Column{ColumnName: name}
}

func And(exprs ...Expr) Expr {
	return and(exprs)
}

func and(exprs []Expr) Expr {
	if len(exprs) == 0 {
		panic("no expressions")
	}
	if len(exprs) == 1 {
		return exprs[0]
	}
	if len(exprs) == 2 {
		return &BinaryExpr{
			Left:  exprs[0],
			Op:    AndOp,
			Right: exprs[1],
		}
	}

	return &BinaryExpr{
		Left:  exprs[0],
		Op:    AndOp,
		Right: and(exprs[1:]),
	}
}

type DynamicColumn struct {
	ColumnName string
}

func (c DynamicColumn) MarshalJSON() ([]byte, error) {
	type dynamicColumnJSON struct {
		Expr string
		Name string
	}
	return json.Marshal(dynamicColumnJSON{
		Expr: "dynamicColumn",
		Name: c.ColumnName,
	})
}

func (c *DynamicColumn) UnmarshalJSON(data []byte) error {
	type dynamicColumnJSON struct {
		Expr string
		Name string
	}
	var dcj dynamicColumnJSON
	err := json.Unmarshal(data, &dcj)
	if err != nil {
		return err
	}
	c.ColumnName = dcj.Name
	return nil
}

func DynCol(name string) *DynamicColumn {
	return &DynamicColumn{ColumnName: name}
}

func (c DynamicColumn) DataType(s *dynparquet.Schema) arrow.DataType {
	colDef, found := s.ColumnByName(c.ColumnName)
	if !found {
		panic("column not found")
	}

	return ParquetNodeToType(colDef.StorageLayout)
}

func (c DynamicColumn) ColumnsUsed() []ColumnMatcher {
	return []ColumnMatcher{c.Matcher()}
}

func (c DynamicColumn) Matcher() ColumnMatcher {
	return DynamicColumnMatcher{ColumnName: c.ColumnName}
}

func (c DynamicColumn) Name() string {
	return c.ColumnName
}

func (c DynamicColumn) Accept(visitor Visitor) bool {
	return visitor.PreVisit(&c) && visitor.PostVisit(&c)
}

func Cols(names ...string) []Expr {
	exprs := make([]Expr, len(names))
	for i, name := range names {
		exprs[i] = Col(name)
	}
	return exprs
}

type LiteralExpr struct {
	Value scalar.Scalar
}

func (e LiteralExpr) MarshalJSON() ([]byte, error) {
	type literalExprJSON struct {
		ValueType string
		Value     string
	}
	return json.Marshal(literalExprJSON{
		Value:     e.Value.String(),
		ValueType: reflect.TypeOf(e.Value).String(),
	})
}

func (e *LiteralExpr) UnmarshalJSON(data []byte) error {
	type literalExprJSON struct {
		ValueType string
		Value     interface{}
	}
	var literal literalExprJSON
	err := json.Unmarshal(data, &literal)
	if err != nil {
		return err
	}
	switch literal.ValueType {
	case "*scalar.String":
		e.Value = scalar.MakeScalar(literal.Value)
	}

	return nil
}

func Literal(v interface{}) *LiteralExpr {
	return &LiteralExpr{
		Value: scalar.MakeScalar(v),
	}
}

func (e LiteralExpr) DataType(_ *dynparquet.Schema) arrow.DataType {
	return e.Value.DataType()
}

func (e LiteralExpr) Name() string {
	return e.Value.String()
}

func (e LiteralExpr) Accept(visitor Visitor) bool {
	continu := visitor.PreVisit(&e)
	if !continu {
		return false
	}

	return visitor.PostVisit(&e)
}

func (e LiteralExpr) ColumnsUsed() []ColumnMatcher { return nil }

func (e LiteralExpr) Matcher() ColumnMatcher { return StaticColumnMatcher{ColumnName: e.Name()} }

type AggregationFunction struct {
	Func AggFunc
	Expr Expr
}

func (f AggregationFunction) MarshalJSON() ([]byte, error) {
	type aggregationFunctionJSON struct {
		ExprType string
		Expr     Expr
		FuncType string
		Func     AggFunc
	}
	return json.Marshal(aggregationFunctionJSON{
		ExprType: reflect.TypeOf(f.Expr).String(),
		Expr:     f.Expr,
		FuncType: reflect.TypeOf(f.Func).String(),
		Func:     f.Func,
	})
}

func (f *AggregationFunction) UnmarshalJSON(data []byte) error {
	type aggregationFunctionJSON struct {
		ExprType string
		Expr     json.RawMessage
		FuncType string
		Func     json.RawMessage
	}
	var afj aggregationFunctionJSON
	err := json.Unmarshal(data, &afj)
	if err != nil {
		return err
	}
	switch afj.ExprType {
	default:
		panic(fmt.Sprintf("implement Unmarshalling for %v", afj.ExprType))
	}

	switch afj.FuncType {
	default:
		panic(fmt.Sprintf("implement Unmarshalling for %v", afj.FuncType))
	}

	return nil
}

func (f AggregationFunction) DataType(s *dynparquet.Schema) arrow.DataType {
	return f.Expr.DataType(s)
}

func (f AggregationFunction) Accept(visitor Visitor) bool {
	continu := visitor.PreVisit(&f)
	if !continu {
		return false
	}

	continu = f.Expr.Accept(visitor)
	if !continu {
		return false
	}

	return visitor.PostVisit(&f)
}

func (f AggregationFunction) Name() string {
	return f.Func.String() + "(" + f.Expr.Name() + ")"
}

func (f AggregationFunction) ColumnsUsed() []ColumnMatcher {
	return f.Expr.ColumnsUsed()
}

func (f AggregationFunction) Matcher() ColumnMatcher {
	return StaticColumnMatcher{ColumnName: f.Name()}
}

type AggFunc int

const (
	SumAggFunc AggFunc = iota
)

func (f AggFunc) String() string {
	switch f {
	case SumAggFunc:
		return "sum"
	default:
		return "unknown"
	}
}

func Sum(expr Expr) *AggregationFunction {
	return &AggregationFunction{
		Func: SumAggFunc,
		Expr: expr,
	}
}

type AliasExpr struct {
	Expr  Expr
	Alias string
}

func (e *AliasExpr) MarshalJSON() ([]byte, error) {
	type aliasExprJSON struct {
		ExprType string
		Expr     Expr
		Alias    string
	}
	return json.Marshal(aliasExprJSON{
		ExprType: reflect.TypeOf(e.Expr).String(),
		Expr:     e.Expr,
		Alias:    e.Alias,
	})
}

func (e *AliasExpr) UnmarshalJSON(data []byte) error {
	type aliasExprJSON struct {
		ExprType string
		Expr     json.RawMessage
		Alias    string
	}
	var aej aliasExprJSON
	err := json.Unmarshal(data, &aej)
	if err != nil {
		return err
	}
	e.Alias = aej.Alias

	switch aej.ExprType {
	case "*logicalplan.LiteralExpr":
		var literal LiteralExpr
		err := json.Unmarshal(aej.Expr, &literal)
		if err != nil {
			return err
		}
		e.Expr = &literal
	}

	return nil
}

func (e AliasExpr) DataType(s *dynparquet.Schema) arrow.DataType {
	return e.Expr.DataType(s)
}

func (e AliasExpr) Name() string {
	return e.Alias
}

func (e AliasExpr) ColumnsUsed() []ColumnMatcher {
	return e.Expr.ColumnsUsed()
}

func (e AliasExpr) Matcher() ColumnMatcher {
	return StaticColumnMatcher{ColumnName: e.Name()}
}

func (e AliasExpr) Accept(visitor Visitor) bool {
	continu := visitor.PreVisit(&e)
	if !continu {
		return false
	}

	continu = e.Expr.Accept(visitor)
	if !continu {
		return false
	}

	return visitor.PostVisit(&e)
}

func (f AggregationFunction) Alias(alias string) *AliasExpr {
	return &AliasExpr{
		Expr:  &f,
		Alias: alias,
	}
}
