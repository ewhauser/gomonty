package monty

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"math/big"
	"slices"
	"strings"
	"time"
)

// ValueKind identifies the Monty value variant held by a Value.
type ValueKind string

const (
	valueKindNone       ValueKind = "none"
	valueKindEllipsis   ValueKind = "ellipsis"
	valueKindBool       ValueKind = "bool"
	valueKindInt        ValueKind = "int"
	valueKindBigInt     ValueKind = "big_int"
	valueKindFloat      ValueKind = "float"
	valueKindString     ValueKind = "string"
	valueKindBytes      ValueKind = "bytes"
	valueKindList       ValueKind = "list"
	valueKindTuple      ValueKind = "tuple"
	valueKindNamedTuple ValueKind = "named_tuple"
	valueKindDict       ValueKind = "dict"
	valueKindSet        ValueKind = "set"
	valueKindFrozenSet  ValueKind = "frozen_set"
	valueKindException  ValueKind = "exception"
	valueKindPath       ValueKind = "path"
	valueKindDataclass  ValueKind = "dataclass"
	valueKindFunction   ValueKind = "function"
	valueKindRepr       ValueKind = "repr"
	valueKindCycle      ValueKind = "cycle"
	valueKindDate       ValueKind = "date"
	valueKindDateTime   ValueKind = "datetime"
	valueKindTimeDelta  ValueKind = "timedelta"
	valueKindTimeZone   ValueKind = "timezone"
)

// Value is the public tagged union used by the Go bindings.
//
// Primitive Go values round-trip directly where safe:
// `nil`, `bool`, signed integers, `float64`, `string`, and `[]byte`.
// Non-native Monty shapes remain explicit through helper structs such as
// Dict, Tuple, NamedTuple, Path, Dataclass, Function, and Exception.
type Value struct {
	kind ValueKind
	data any
}

// Pair preserves insertion order and non-string keys for dict-like values.
type Pair struct {
	Key   Value `json:"key"`
	Value Value `json:"value"`
}

// Dict preserves insertion order and non-string keys.
type Dict []Pair

// Tuple is the explicit tuple shape used by the bindings.
type Tuple []Value

// Set is the explicit set shape used by the bindings.
type Set []Value

// FrozenSet is the explicit frozenset shape used by the bindings.
type FrozenSet []Value

// Date represents a Python datetime.date value.
type Date struct {
	Year  int32 `json:"year"`
	Month uint8 `json:"month"`
	Day   uint8 `json:"day"`
}

// DateTime represents a Python datetime.datetime value.
type DateTime struct {
	Year          int32   `json:"year"`
	Month         uint8   `json:"month"`
	Day           uint8   `json:"day"`
	Hour          uint8   `json:"hour"`
	Minute        uint8   `json:"minute"`
	Second        uint8   `json:"second"`
	Microsecond   uint32  `json:"microsecond"`
	OffsetSeconds *int32  `json:"offset_seconds,omitempty"`
	TimezoneName  *string `json:"timezone_name,omitempty"`
}

// TimeDelta represents a Python datetime.timedelta value.
type TimeDelta struct {
	Days         int32 `json:"days"`
	Seconds      int32 `json:"seconds"`
	Microseconds int32 `json:"microseconds"`
}

// TimeZone represents a Python datetime.timezone fixed-offset value.
type TimeZone struct {
	OffsetSeconds int32   `json:"offset_seconds"`
	Name          *string `json:"name,omitempty"`
}

// NamedTuple carries the explicit named-tuple wire shape.
type NamedTuple struct {
	TypeName   string   `json:"type_name"`
	FieldNames []string `json:"field_names"`
	Values     []Value  `json:"values"`
}

// StatResult is the explicit host helper for `os.stat_result`-compatible values.
type StatResult struct {
	Mode  int64
	Ino   int64
	Dev   int64
	Nlink int64
	UID   int64
	GID   int64
	Size  int64
	Atime float64
	Mtime float64
	Ctime float64
}

// Path is an explicit Monty path value.
type Path string

// Function is the explicit external-function value used during name lookup.
type Function struct {
	Name      string  `json:"name"`
	Docstring *string `json:"docstring,omitempty"`
}

// Dataclass is the explicit host-side dataclass value shape.
//
// Methods are host-only metadata and are intentionally omitted from the wire
// format so Rust never stores Go function pointers or opaque Go values.
type Dataclass struct {
	Name       string                   `json:"name"`
	TypeID     uint64                   `json:"type_id"`
	FieldNames []string                 `json:"field_names"`
	Attrs      Dict                     `json:"attrs"`
	Frozen     bool                     `json:"frozen"`
	Methods    map[string]MethodHandler `json:"-"`
}

// Exception is both a first-class Monty value and the explicit exception shape
// returned by host callbacks.
type Exception struct {
	Type string  `json:"exc_type"`
	Arg  *string `json:"arg,omitempty"`
}

// Call describes an external function or method invocation from Monty.
type Call struct {
	FunctionName string
	Args         []Value
	Kwargs       Dict
	CallID       uint32
	IsMethodCall bool
}

// OSFunction names a host OS callback that Monty requested.
type OSFunction string

const (
	OSPathExists     OSFunction = "Path.exists"
	OSPathIsFile     OSFunction = "Path.is_file"
	OSPathIsDir      OSFunction = "Path.is_dir"
	OSPathIsSymlink  OSFunction = "Path.is_symlink"
	OSPathReadText   OSFunction = "Path.read_text"
	OSPathReadBytes  OSFunction = "Path.read_bytes"
	OSPathWriteText  OSFunction = "Path.write_text"
	OSPathWriteBytes OSFunction = "Path.write_bytes"
	OSPathMkdir      OSFunction = "Path.mkdir"
	OSPathUnlink     OSFunction = "Path.unlink"
	OSPathRmdir      OSFunction = "Path.rmdir"
	OSPathIterdir    OSFunction = "Path.iterdir"
	OSPathStat       OSFunction = "Path.stat"
	OSPathRename     OSFunction = "Path.rename"
	OSPathResolve    OSFunction = "Path.resolve"
	OSPathAbsolute   OSFunction = "Path.absolute"
	OSGetenv         OSFunction = "os.getenv"
	OSGetEnviron     OSFunction = "os.environ"
)

// OSCall describes an OS-level callback requested by Monty.
type OSCall struct {
	Function OSFunction
	Args     []Value
	Kwargs   Dict
	CallID   uint32
}

// Waiter represents a pending external result that will be resolved later.
type Waiter interface {
	Wait(context.Context) Result
}

// ExternalFunction handles a host external function callback.
type ExternalFunction func(context.Context, Call) (Result, error)

// MethodHandler handles a host dataclass method callback.
type MethodHandler = ExternalFunction

// OSHandler handles a host OS callback.
type OSHandler func(context.Context, OSCall) (Result, error)

// PrintCallback receives aggregated stdout text emitted during each execution step.
type PrintCallback func(stream string, text string)

// WriterPrintCallback writes captured stdout to an io.Writer.
func WriterPrintCallback(w io.Writer) PrintCallback {
	return func(_ string, text string) {
		_, _ = io.WriteString(w, text)
	}
}

type resultKind uint8

const (
	resultKindReturn resultKind = iota
	resultKindException
	resultKindPending
)

// Result is the explicit host callback result union.
//
// The zero value is treated as `Return(None())`, which keeps simple handlers
// ergonomic while preserving an explicit pending/exception path.
type Result struct {
	kind      resultKind
	value     Value
	exception *Exception
	waiter    Waiter
}

// Return constructs a successful callback result.
func Return(value Value) Result {
	return Result{
		kind:  resultKindReturn,
		value: value,
	}
}

// Raise constructs a callback result that raises an explicit exception.
func Raise(exception Exception) Result {
	return Result{
		kind:      resultKindException,
		exception: &exception,
	}
}

// Pending constructs a callback result backed by a waiter.
func Pending(waiter Waiter) Result {
	return Result{
		kind:   resultKindPending,
		waiter: waiter,
	}
}

// ResourceLimits controls Monty execution limits.
//
// A zero field value leaves that limit unset.
type ResourceLimits struct {
	MaxAllocations    int
	MaxDuration       time.Duration
	MaxMemory         int
	GCInterval        int
	MaxRecursionDepth int
}

// CompileOptions configures runner compilation.
type CompileOptions struct {
	ScriptName     string
	Inputs         []string
	TypeCheck      bool
	TypeCheckStubs string
}

// StartOptions configures low-level runner execution.
type StartOptions struct {
	Inputs map[string]Value
	Limits *ResourceLimits
}

// ReplOptions configures REPL construction.
type ReplOptions struct {
	ScriptName string
	Limits     *ResourceLimits
}

// FeedStartOptions configures low-level REPL snippet execution.
type FeedStartOptions struct {
	Inputs map[string]Value
}

// RunOptions configures the high-level runner helper loop.
type RunOptions struct {
	Inputs    map[string]Value
	Functions map[string]ExternalFunction
	OS        OSHandler
	Print     PrintCallback
	Limits    *ResourceLimits
}

// FeedOptions configures the high-level REPL helper loop.
type FeedOptions struct {
	Inputs    map[string]Value
	Functions map[string]ExternalFunction
	OS        OSHandler
	Print     PrintCallback
}

// None returns the Python None value.
func None() Value {
	return Value{}
}

// Ellipsis returns the Python Ellipsis value.
func Ellipsis() Value {
	return Value{kind: valueKindEllipsis}
}

// Bool returns a bool value.
func Bool(value bool) Value {
	return Value{kind: valueKindBool, data: value}
}

// Int returns a signed integer value.
func Int(value int64) Value {
	return Value{kind: valueKindInt, data: value}
}

// BigInt returns an arbitrary-precision integer value.
func BigInt(value *big.Int) Value {
	if value == nil {
		return Value{kind: valueKindBigInt, data: big.NewInt(0)}
	}
	return Value{kind: valueKindBigInt, data: new(big.Int).Set(value)}
}

// Float returns a float value.
func Float(value float64) Value {
	return Value{kind: valueKindFloat, data: value}
}

// String returns a string value.
func String(value string) Value {
	return Value{kind: valueKindString, data: value}
}

// Bytes returns a bytes value.
func Bytes(value []byte) Value {
	return Value{kind: valueKindBytes, data: slices.Clone(value)}
}

// List returns a list value.
func List(items ...Value) Value {
	return Value{kind: valueKindList, data: slices.Clone(items)}
}

// TupleValue returns a tuple value.
func TupleValue(items ...Value) Value {
	return Value{kind: valueKindTuple, data: Tuple(slices.Clone(items))}
}

// NamedTupleValue returns a named-tuple value.
func NamedTupleValue(value NamedTuple) Value {
	value.FieldNames = slices.Clone(value.FieldNames)
	value.Values = slices.Clone(value.Values)
	return Value{kind: valueKindNamedTuple, data: value}
}

// DictValue returns a dict value.
func DictValue(items Dict) Value {
	return Value{kind: valueKindDict, data: slices.Clone(items)}
}

// SetValue returns a set value.
func SetValue(items Set) Value {
	return Value{kind: valueKindSet, data: slices.Clone(items)}
}

// FrozenSetValue returns a frozenset value.
func FrozenSetValue(items FrozenSet) Value {
	return Value{kind: valueKindFrozenSet, data: slices.Clone(items)}
}

// ExceptionValue returns an exception value.
func ExceptionValue(exception Exception) Value {
	return Value{kind: valueKindException, data: exception}
}

// PathValue returns a path value.
func PathValue(path Path) Value {
	return Value{kind: valueKindPath, data: path}
}

// DataclassValue returns a dataclass value.
func DataclassValue(value Dataclass) Value {
	value.FieldNames = slices.Clone(value.FieldNames)
	value.Attrs = slices.Clone(value.Attrs)
	return Value{kind: valueKindDataclass, data: value}
}

// FunctionValue returns an external-function value.
func FunctionValue(function Function) Value {
	return Value{kind: valueKindFunction, data: function}
}

// ReprValue returns an output-only repr placeholder.
func ReprValue(value string) Value {
	return Value{kind: valueKindRepr, data: value}
}

// CycleValue returns an output-only cycle placeholder.
func CycleValue(value string) Value {
	return Value{kind: valueKindCycle, data: value}
}

// DateValue returns a date value.
func DateValue(date Date) Value {
	return Value{kind: valueKindDate, data: date}
}

// DateTimeValue returns a datetime value.
func DateTimeValue(dt DateTime) Value {
	return Value{kind: valueKindDateTime, data: dt}
}

// TimeDeltaValue returns a timedelta value.
func TimeDeltaValue(td TimeDelta) Value {
	return Value{kind: valueKindTimeDelta, data: td}
}

// TimeZoneValue returns a timezone value.
func TimeZoneValue(tz TimeZone) Value {
	return Value{kind: valueKindTimeZone, data: tz}
}

// Kind reports the value variant.
func (v Value) Kind() ValueKind {
	if v.kind == "" {
		return valueKindNone
	}
	return v.kind
}

// Raw exposes the underlying variant payload.
func (v Value) Raw() any {
	return v.data
}

// Dataclass returns the dataclass payload if present.
func (v Value) Dataclass() (Dataclass, bool) {
	value, ok := v.data.(Dataclass)
	return value, ok
}

// StatResult returns a stat-result payload when the value is a compatible named tuple.
func (v Value) StatResult() (StatResult, bool) {
	if v.Kind() != valueKindNamedTuple {
		return StatResult{}, false
	}
	namedTuple := v.data.(NamedTuple)
	if namedTuple.TypeName != "StatResult" || len(namedTuple.Values) != 10 {
		return StatResult{}, false
	}
	intValue := func(index int) (int64, bool) {
		value := namedTuple.Values[index]
		if value.Kind() != valueKindInt {
			return 0, false
		}
		return value.data.(int64), true
	}
	floatValue := func(index int) (float64, bool) {
		value := namedTuple.Values[index]
		if value.Kind() != valueKindFloat {
			return 0, false
		}
		return value.data.(float64), true
	}

	mode, ok := intValue(0)
	if !ok {
		return StatResult{}, false
	}
	ino, ok := intValue(1)
	if !ok {
		return StatResult{}, false
	}
	dev, ok := intValue(2)
	if !ok {
		return StatResult{}, false
	}
	nlink, ok := intValue(3)
	if !ok {
		return StatResult{}, false
	}
	uid, ok := intValue(4)
	if !ok {
		return StatResult{}, false
	}
	gid, ok := intValue(5)
	if !ok {
		return StatResult{}, false
	}
	size, ok := intValue(6)
	if !ok {
		return StatResult{}, false
	}
	atime, ok := floatValue(7)
	if !ok {
		return StatResult{}, false
	}
	mtime, ok := floatValue(8)
	if !ok {
		return StatResult{}, false
	}
	ctime, ok := floatValue(9)
	if !ok {
		return StatResult{}, false
	}

	return StatResult{
		Mode:  mode,
		Ino:   ino,
		Dev:   dev,
		Nlink: nlink,
		UID:   uid,
		GID:   gid,
		Size:  size,
		Atime: atime,
		Mtime: mtime,
		Ctime: ctime,
	}, true
}

// Function returns the function payload if present.
func (v Value) Function() (Function, bool) {
	value, ok := v.data.(Function)
	return value, ok
}

// Exception returns the exception payload if present.
func (v Value) Exception() (Exception, bool) {
	value, ok := v.data.(Exception)
	return value, ok
}

// Date returns the date payload if present.
func (v Value) Date() (Date, bool) {
	value, ok := v.data.(Date)
	return value, ok
}

// DateTime returns the datetime payload if present.
func (v Value) DateTime() (DateTime, bool) {
	value, ok := v.data.(DateTime)
	return value, ok
}

// TimeDelta returns the timedelta payload if present.
func (v Value) TimeDelta() (TimeDelta, bool) {
	value, ok := v.data.(TimeDelta)
	return value, ok
}

// TimeZone returns the timezone payload if present.
func (v Value) TimeZone() (TimeZone, bool) {
	value, ok := v.data.(TimeZone)
	return value, ok
}

// ValueOf converts a supported Go value into a Monty Value.
func ValueOf(value any) (Value, error) {
	switch value := value.(type) {
	case nil:
		return None(), nil
	case Value:
		return value, nil
	case bool:
		return Bool(value), nil
	case int:
		return Int(int64(value)), nil
	case int8:
		return Int(int64(value)), nil
	case int16:
		return Int(int64(value)), nil
	case int32:
		return Int(int64(value)), nil
	case int64:
		return Int(value), nil
	case uint:
		if uint64(value) <= math.MaxInt64 {
			return Int(int64(value)), nil
		}
		bigValue := new(big.Int)
		bigValue.SetUint64(uint64(value))
		return BigInt(bigValue), nil
	case uint8:
		return Int(int64(value)), nil
	case uint16:
		return Int(int64(value)), nil
	case uint32:
		return Int(int64(value)), nil
	case uint64:
		if value <= math.MaxInt64 {
			return Int(int64(value)), nil
		}
		bigValue := new(big.Int)
		bigValue.SetUint64(value)
		return BigInt(bigValue), nil
	case float32:
		return Float(float64(value)), nil
	case float64:
		return Float(value), nil
	case string:
		return String(value), nil
	case []byte:
		return Bytes(value), nil
	case []Value:
		return List(value...), nil
	case []any:
		items := make([]Value, 0, len(value))
		for _, item := range value {
			converted, err := ValueOf(item)
			if err != nil {
				return Value{}, err
			}
			items = append(items, converted)
		}
		return List(items...), nil
	case Tuple:
		return Value{kind: valueKindTuple, data: value}, nil
	case NamedTuple:
		return NamedTupleValue(value), nil
	case Dict:
		return DictValue(value), nil
	case Set:
		return SetValue(value), nil
	case FrozenSet:
		return FrozenSetValue(value), nil
	case Path:
		return PathValue(value), nil
	case Dataclass:
		return DataclassValue(value), nil
	case Function:
		return FunctionValue(value), nil
	case Exception:
		return ExceptionValue(value), nil
	case Date:
		return DateValue(value), nil
	case DateTime:
		return DateTimeValue(value), nil
	case TimeDelta:
		return TimeDeltaValue(value), nil
	case TimeZone:
		return TimeZoneValue(value), nil
	case *big.Int:
		return BigInt(value), nil
	case big.Int:
		return BigInt(&value), nil
	case map[string]Value:
		items := make(Dict, 0, len(value))
		for key, item := range value {
			items = append(items, Pair{Key: String(key), Value: item})
		}
		return DictValue(items), nil
	case StatResult:
		return NamedTupleValue(value.namedTuple()), nil
	case map[string]any:
		items := make(Dict, 0, len(value))
		for key, item := range value {
			converted, err := ValueOf(item)
			if err != nil {
				return Value{}, err
			}
			items = append(items, Pair{Key: String(key), Value: converted})
		}
		return DictValue(items), nil
	default:
		return Value{}, fmt.Errorf("unsupported Monty value type %T", value)
	}
}

// MustValueOf converts a Go value and panics if conversion fails.
func MustValueOf(value any) Value {
	converted, err := ValueOf(value)
	if err != nil {
		panic(err)
	}
	return converted
}

// Error formats the exception as `Type: message`.
func (e Exception) Error() string {
	if e.Arg == nil || *e.Arg == "" {
		return e.Type
	}
	return e.Type + ": " + *e.Arg
}

// FromError converts an arbitrary Go error into an explicit exception shape.
func (e Exception) FromError(err error) Exception {
	return exceptionFromError(err)
}

func exceptionFromError(err error) Exception {
	if err == nil {
		return Exception{Type: "RuntimeError"}
	}
	var explicit Exception
	if errors.As(err, &explicit) {
		return explicit
	}
	type causer interface {
		Unwrap() error
	}
	if wrapped, ok := err.(causer); ok && wrapped.Unwrap() != nil {
		var nested Exception
		if errors.As(wrapped.Unwrap(), &nested) {
			return nested
		}
	}
	message := err.Error()
	return Exception{
		Type: "RuntimeError",
		Arg:  &message,
	}
}

func (r Result) wireValue() Value {
	if r.kind == resultKindReturn {
		return r.value
	}
	return None()
}

// Value returns the success value for a return result.
func (r Result) Value() Value {
	return r.wireValue()
}

func (r Result) wireException() *Exception {
	if r.kind == resultKindException {
		return r.exception
	}
	return nil
}

// Raised returns the explicit exception payload for an exception result.
func (r Result) Raised() (*Exception, bool) {
	exception := r.wireException()
	return exception, exception != nil
}

func (r Result) wirePending() bool {
	return r.kind == resultKindPending
}

// IsPending reports whether the result is pending.
func (r Result) IsPending() bool {
	return r.wirePending()
}

func (r Result) waiterValue() Waiter {
	return r.waiter
}

// Waiter returns the waiter attached to a pending result.
func (r Result) Waiter() Waiter {
	return r.waiterValue()
}

func (v Value) MarshalJSON() ([]byte, error) {
	switch v.Kind() {
	case valueKindNone:
		return json.Marshal(struct {
			Kind ValueKind `json:"kind"`
		}{Kind: valueKindNone})
	case valueKindEllipsis:
		return json.Marshal(struct {
			Kind ValueKind `json:"kind"`
		}{Kind: valueKindEllipsis})
	case valueKindBool:
		return json.Marshal(struct {
			Kind  ValueKind `json:"kind"`
			Value bool      `json:"value"`
		}{Kind: valueKindBool, Value: v.data.(bool)})
	case valueKindInt:
		return json.Marshal(struct {
			Kind  ValueKind `json:"kind"`
			Value int64     `json:"value"`
		}{Kind: valueKindInt, Value: v.data.(int64)})
	case valueKindBigInt:
		return json.Marshal(struct {
			Kind  ValueKind `json:"kind"`
			Value string    `json:"value"`
		}{Kind: valueKindBigInt, Value: v.data.(*big.Int).String()})
	case valueKindFloat:
		return json.Marshal(struct {
			Kind  ValueKind `json:"kind"`
			Value float64   `json:"value"`
		}{Kind: valueKindFloat, Value: v.data.(float64)})
	case valueKindString:
		return json.Marshal(struct {
			Kind  ValueKind `json:"kind"`
			Value string    `json:"value"`
		}{Kind: valueKindString, Value: v.data.(string)})
	case valueKindBytes:
		return json.Marshal(struct {
			Kind  ValueKind `json:"kind"`
			Value []byte    `json:"value"`
		}{Kind: valueKindBytes, Value: v.data.([]byte)})
	case valueKindList:
		return json.Marshal(struct {
			Kind  ValueKind `json:"kind"`
			Items []Value   `json:"items"`
		}{Kind: valueKindList, Items: v.data.([]Value)})
	case valueKindTuple:
		return json.Marshal(struct {
			Kind  ValueKind `json:"kind"`
			Items Tuple     `json:"items"`
		}{Kind: valueKindTuple, Items: v.data.(Tuple)})
	case valueKindNamedTuple:
		payload := v.data.(NamedTuple)
		return json.Marshal(struct {
			Kind       ValueKind `json:"kind"`
			TypeName   string    `json:"type_name"`
			FieldNames []string  `json:"field_names"`
			Values     []Value   `json:"values"`
		}{
			Kind:       valueKindNamedTuple,
			TypeName:   payload.TypeName,
			FieldNames: payload.FieldNames,
			Values:     payload.Values,
		})
	case valueKindDict:
		return json.Marshal(struct {
			Kind  ValueKind `json:"kind"`
			Items Dict      `json:"items"`
		}{Kind: valueKindDict, Items: v.data.(Dict)})
	case valueKindSet:
		return json.Marshal(struct {
			Kind  ValueKind `json:"kind"`
			Items Set       `json:"items"`
		}{Kind: valueKindSet, Items: v.data.(Set)})
	case valueKindFrozenSet:
		return json.Marshal(struct {
			Kind  ValueKind `json:"kind"`
			Items FrozenSet `json:"items"`
		}{Kind: valueKindFrozenSet, Items: v.data.(FrozenSet)})
	case valueKindException:
		payload := v.data.(Exception)
		return json.Marshal(struct {
			Kind ValueKind `json:"kind"`
			Type string    `json:"exc_type"`
			Arg  *string   `json:"arg,omitempty"`
		}{
			Kind: valueKindException,
			Type: payload.Type,
			Arg:  payload.Arg,
		})
	case valueKindPath:
		return json.Marshal(struct {
			Kind  ValueKind `json:"kind"`
			Value string    `json:"value"`
		}{Kind: valueKindPath, Value: string(v.data.(Path))})
	case valueKindDataclass:
		payload := v.data.(Dataclass)
		return json.Marshal(struct {
			Kind       ValueKind `json:"kind"`
			Name       string    `json:"name"`
			TypeID     uint64    `json:"type_id"`
			FieldNames []string  `json:"field_names"`
			Attrs      Dict      `json:"attrs"`
			Frozen     bool      `json:"frozen"`
		}{
			Kind:       valueKindDataclass,
			Name:       payload.Name,
			TypeID:     payload.TypeID,
			FieldNames: payload.FieldNames,
			Attrs:      payload.Attrs,
			Frozen:     payload.Frozen,
		})
	case valueKindFunction:
		payload := v.data.(Function)
		return json.Marshal(struct {
			Kind      ValueKind `json:"kind"`
			Name      string    `json:"name"`
			Docstring *string   `json:"docstring,omitempty"`
		}{
			Kind:      valueKindFunction,
			Name:      payload.Name,
			Docstring: payload.Docstring,
		})
	case valueKindRepr:
		return json.Marshal(struct {
			Kind  ValueKind `json:"kind"`
			Value string    `json:"value"`
		}{Kind: valueKindRepr, Value: v.data.(string)})
	case valueKindCycle:
		return json.Marshal(struct {
			Kind        ValueKind `json:"kind"`
			Placeholder string    `json:"placeholder"`
		}{Kind: valueKindCycle, Placeholder: v.data.(string)})
	case valueKindDate:
		payload := v.data.(Date)
		return json.Marshal(struct {
			Kind  ValueKind `json:"kind"`
			Year  int32     `json:"year"`
			Month uint8     `json:"month"`
			Day   uint8     `json:"day"`
		}{Kind: valueKindDate, Year: payload.Year, Month: payload.Month, Day: payload.Day})
	case valueKindDateTime:
		payload := v.data.(DateTime)
		return json.Marshal(struct {
			Kind          ValueKind `json:"kind"`
			Year          int32     `json:"year"`
			Month         uint8     `json:"month"`
			Day           uint8     `json:"day"`
			Hour          uint8     `json:"hour"`
			Minute        uint8     `json:"minute"`
			Second        uint8     `json:"second"`
			Microsecond   uint32    `json:"microsecond"`
			OffsetSeconds *int32    `json:"offset_seconds,omitempty"`
			TimezoneName  *string   `json:"timezone_name,omitempty"`
		}{
			Kind: valueKindDateTime, Year: payload.Year, Month: payload.Month,
			Day: payload.Day, Hour: payload.Hour, Minute: payload.Minute,
			Second: payload.Second, Microsecond: payload.Microsecond,
			OffsetSeconds: payload.OffsetSeconds, TimezoneName: payload.TimezoneName,
		})
	case valueKindTimeDelta:
		payload := v.data.(TimeDelta)
		return json.Marshal(struct {
			Kind         ValueKind `json:"kind"`
			Days         int32     `json:"days"`
			Seconds      int32     `json:"seconds"`
			Microseconds int32     `json:"microseconds"`
		}{Kind: valueKindTimeDelta, Days: payload.Days, Seconds: payload.Seconds, Microseconds: payload.Microseconds})
	case valueKindTimeZone:
		payload := v.data.(TimeZone)
		return json.Marshal(struct {
			Kind          ValueKind `json:"kind"`
			OffsetSeconds int32     `json:"offset_seconds"`
			Name          *string   `json:"name,omitempty"`
		}{Kind: valueKindTimeZone, OffsetSeconds: payload.OffsetSeconds, Name: payload.Name})
	default:
		return nil, fmt.Errorf("unsupported value kind %q", v.kind)
	}
}

// UnmarshalJSON decodes the stable Rust/Go wire schema for values.
func (v *Value) UnmarshalJSON(data []byte) error {
	var envelope struct {
		Kind ValueKind `json:"kind"`
	}
	if err := json.Unmarshal(data, &envelope); err != nil {
		return err
	}

	switch envelope.Kind {
	case valueKindNone:
		*v = None()
	case valueKindEllipsis:
		*v = Ellipsis()
	case valueKindBool:
		var payload struct {
			Value bool `json:"value"`
		}
		if err := json.Unmarshal(data, &payload); err != nil {
			return err
		}
		*v = Bool(payload.Value)
	case valueKindInt:
		var payload struct {
			Value int64 `json:"value"`
		}
		if err := json.Unmarshal(data, &payload); err != nil {
			return err
		}
		*v = Int(payload.Value)
	case valueKindBigInt:
		var payload struct {
			Value string `json:"value"`
		}
		if err := json.Unmarshal(data, &payload); err != nil {
			return err
		}
		bigValue, ok := new(big.Int).SetString(payload.Value, 10)
		if !ok {
			return fmt.Errorf("invalid bigint %q", payload.Value)
		}
		*v = BigInt(bigValue)
	case valueKindFloat:
		var payload struct {
			Value float64 `json:"value"`
		}
		if err := json.Unmarshal(data, &payload); err != nil {
			return err
		}
		*v = Float(payload.Value)
	case valueKindString:
		var payload struct {
			Value string `json:"value"`
		}
		if err := json.Unmarshal(data, &payload); err != nil {
			return err
		}
		*v = String(payload.Value)
	case valueKindBytes:
		var payload struct {
			Value []byte `json:"value"`
		}
		if err := json.Unmarshal(data, &payload); err != nil {
			return err
		}
		*v = Bytes(payload.Value)
	case valueKindList:
		var payload struct {
			Items []Value `json:"items"`
		}
		if err := json.Unmarshal(data, &payload); err != nil {
			return err
		}
		*v = List(payload.Items...)
	case valueKindTuple:
		var payload struct {
			Items Tuple `json:"items"`
		}
		if err := json.Unmarshal(data, &payload); err != nil {
			return err
		}
		*v = Value{kind: valueKindTuple, data: payload.Items}
	case valueKindNamedTuple:
		var payload NamedTuple
		if err := json.Unmarshal(data, &payload); err != nil {
			return err
		}
		*v = NamedTupleValue(payload)
	case valueKindDict:
		var payload struct {
			Items Dict `json:"items"`
		}
		if err := json.Unmarshal(data, &payload); err != nil {
			return err
		}
		*v = DictValue(payload.Items)
	case valueKindSet:
		var payload struct {
			Items Set `json:"items"`
		}
		if err := json.Unmarshal(data, &payload); err != nil {
			return err
		}
		*v = SetValue(payload.Items)
	case valueKindFrozenSet:
		var payload struct {
			Items FrozenSet `json:"items"`
		}
		if err := json.Unmarshal(data, &payload); err != nil {
			return err
		}
		*v = FrozenSetValue(payload.Items)
	case valueKindException:
		var payload struct {
			Type string  `json:"exc_type"`
			Arg  *string `json:"arg"`
		}
		if err := json.Unmarshal(data, &payload); err != nil {
			return err
		}
		*v = ExceptionValue(Exception{Type: payload.Type, Arg: payload.Arg})
	case valueKindPath:
		var payload struct {
			Value string `json:"value"`
		}
		if err := json.Unmarshal(data, &payload); err != nil {
			return err
		}
		*v = PathValue(Path(payload.Value))
	case valueKindDataclass:
		var payload Dataclass
		if err := json.Unmarshal(data, &payload); err != nil {
			return err
		}
		*v = DataclassValue(payload)
	case valueKindFunction:
		var payload Function
		if err := json.Unmarshal(data, &payload); err != nil {
			return err
		}
		*v = FunctionValue(payload)
	case valueKindRepr:
		var payload struct {
			Value string `json:"value"`
		}
		if err := json.Unmarshal(data, &payload); err != nil {
			return err
		}
		*v = ReprValue(payload.Value)
	case valueKindCycle:
		var payload struct {
			Placeholder string `json:"placeholder"`
		}
		if err := json.Unmarshal(data, &payload); err != nil {
			return err
		}
		*v = CycleValue(payload.Placeholder)
	case valueKindDate:
		var payload Date
		if err := json.Unmarshal(data, &payload); err != nil {
			return err
		}
		*v = DateValue(payload)
	case valueKindDateTime:
		var payload DateTime
		if err := json.Unmarshal(data, &payload); err != nil {
			return err
		}
		*v = DateTimeValue(payload)
	case valueKindTimeDelta:
		var payload TimeDelta
		if err := json.Unmarshal(data, &payload); err != nil {
			return err
		}
		*v = TimeDeltaValue(payload)
	case valueKindTimeZone:
		var payload TimeZone
		if err := json.Unmarshal(data, &payload); err != nil {
			return err
		}
		*v = TimeZoneValue(payload)
	default:
		return fmt.Errorf("unknown value kind %q", envelope.Kind)
	}
	return nil
}

func (v Value) String() string {
	switch v.Kind() {
	case valueKindNone:
		return "None"
	case valueKindEllipsis:
		return "Ellipsis"
	case valueKindBool, valueKindInt, valueKindFloat, valueKindString:
		return fmt.Sprintf("%v", v.data)
	case valueKindBigInt:
		return v.data.(*big.Int).String()
	case valueKindBytes:
		return fmt.Sprintf("%q", v.data.([]byte))
	case valueKindPath:
		return string(v.data.(Path))
	case valueKindException:
		return v.data.(Exception).Error()
	case valueKindFunction:
		return v.data.(Function).Name
	case valueKindDate:
		d := v.data.(Date)
		return fmt.Sprintf("%04d-%02d-%02d", d.Year, d.Month, d.Day)
	case valueKindDateTime:
		dt := v.data.(DateTime)
		base := fmt.Sprintf("%04d-%02d-%02d %02d:%02d:%02d", dt.Year, dt.Month, dt.Day, dt.Hour, dt.Minute, dt.Second)
		if dt.Microsecond != 0 {
			base += fmt.Sprintf(".%06d", dt.Microsecond)
		}
		if dt.OffsetSeconds != nil {
			offset := *dt.OffsetSeconds
			sign := "+"
			if offset < 0 {
				sign = "-"
				offset = -offset
			}
			base += fmt.Sprintf("%s%02d:%02d", sign, offset/3600, (offset%3600)/60)
		}
		return base
	case valueKindTimeDelta:
		td := v.data.(TimeDelta)
		return fmt.Sprintf("timedelta(days=%d, seconds=%d, microseconds=%d)", td.Days, td.Seconds, td.Microseconds)
	case valueKindTimeZone:
		tz := v.data.(TimeZone)
		if tz.Name != nil {
			return *tz.Name
		}
		offset := tz.OffsetSeconds
		sign := "+"
		if offset < 0 {
			sign = "-"
			offset = -offset
		}
		return fmt.Sprintf("UTC%s%02d:%02d", sign, offset/3600, (offset%3600)/60)
	default:
		typeName := string(v.Kind())
		typeName = strings.ReplaceAll(typeName, "_", " ")
		return typeName
	}
}

func (s StatResult) namedTuple() NamedTuple {
	return NamedTuple{
		TypeName: "StatResult",
		FieldNames: []string{
			"st_mode",
			"st_ino",
			"st_dev",
			"st_nlink",
			"st_uid",
			"st_gid",
			"st_size",
			"st_atime",
			"st_mtime",
			"st_ctime",
		},
		Values: []Value{
			Int(s.Mode),
			Int(s.Ino),
			Int(s.Dev),
			Int(s.Nlink),
			Int(s.UID),
			Int(s.GID),
			Int(s.Size),
			Float(s.Atime),
			Float(s.Mtime),
			Float(s.Ctime),
		},
	}
}
