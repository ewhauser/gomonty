package monty

import (
	"fmt"
	"math/big"

	"github.com/vmihailenco/msgpack/v5"
)

const wireVersion = 1

const (
	wireValueNone uint8 = iota
	wireValueEllipsis
	wireValueBool
	wireValueInt
	wireValueBigInt
	wireValueFloat
	wireValueString
	wireValueBytes
	wireValueList
	wireValueTuple
	wireValueNamedTuple
	wireValueDict
	wireValueSet
	wireValueFrozenSet
	wireValueException
	wireValuePath
	wireValueDataclass
	wireValueFunction
	wireValueRepr
	wireValueCycle
)

const (
	wireCallResultReturn uint8 = iota
	wireCallResultException
	wireCallResultPending
)

const (
	wireLookupResultValue uint8 = iota
	wireLookupResultUndefined
)

const (
	wireProgressFunctionCall uint8 = iota
	wireProgressNameLookup
	wireProgressFuture
	wireProgressComplete
)

type wirePair struct {
	Key   wireValue `msgpack:"key"`
	Value wireValue `msgpack:"value"`
}

type wireValue struct {
	Kind        uint8       `msgpack:"kind"`
	Bool        bool        `msgpack:"bool,omitempty"`
	Int         int64       `msgpack:"int,omitempty"`
	BigInt      string      `msgpack:"big_int,omitempty"`
	Float       float64     `msgpack:"float,omitempty"`
	String      string      `msgpack:"string,omitempty"`
	Bytes       []byte      `msgpack:"bytes,omitempty"`
	Items       []wireValue `msgpack:"items,omitempty"`
	TypeName    string      `msgpack:"type_name,omitempty"`
	FieldNames  []string    `msgpack:"field_names,omitempty"`
	Values      []wireValue `msgpack:"values,omitempty"`
	Pairs       []wirePair  `msgpack:"pairs,omitempty"`
	ExcType     string      `msgpack:"exc_type,omitempty"`
	Arg         *string     `msgpack:"arg,omitempty"`
	Name        string      `msgpack:"name,omitempty"`
	TypeID      uint64      `msgpack:"type_id,omitempty"`
	Attrs       []wirePair  `msgpack:"attrs,omitempty"`
	Frozen      bool        `msgpack:"frozen,omitempty"`
	Docstring   *string     `msgpack:"docstring,omitempty"`
	Placeholder string      `msgpack:"placeholder,omitempty"`
}

type wireCompileOptions struct {
	Version        uint32   `msgpack:"version"`
	ScriptName     *string  `msgpack:"script_name,omitempty"`
	Inputs         []string `msgpack:"inputs,omitempty"`
	TypeCheck      bool     `msgpack:"type_check,omitempty"`
	TypeCheckStubs *string  `msgpack:"type_check_stubs,omitempty"`
}

type wireResourceLimits struct {
	Version           uint32   `msgpack:"version"`
	MaxAllocations    *int     `msgpack:"max_allocations,omitempty"`
	MaxDurationSecs   *float64 `msgpack:"max_duration_secs,omitempty"`
	MaxMemory         *int     `msgpack:"max_memory,omitempty"`
	GCInterval        *int     `msgpack:"gc_interval,omitempty"`
	MaxRecursionDepth *int     `msgpack:"max_recursion_depth,omitempty"`
}

type wireStartOptions struct {
	Version uint32               `msgpack:"version"`
	Inputs  map[string]wireValue `msgpack:"inputs,omitempty"`
	Limits  *wireResourceLimits  `msgpack:"limits,omitempty"`
}

type wireReplOptions struct {
	Version    uint32              `msgpack:"version"`
	ScriptName *string             `msgpack:"script_name,omitempty"`
	Limits     *wireResourceLimits `msgpack:"limits,omitempty"`
}

type wireFeedOptions struct {
	Version uint32               `msgpack:"version"`
	Inputs  map[string]wireValue `msgpack:"inputs,omitempty"`
}

type callResultPayload struct {
	Kind  string
	Value Value
	Type  string
	Arg   *string
}

type lookupResultPayload struct {
	Kind  string
	Value Value
}

type wireCallResult struct {
	Kind  uint8      `msgpack:"kind"`
	Value *wireValue `msgpack:"value,omitempty"`
	Type  string     `msgpack:"exc_type,omitempty"`
	Arg   *string    `msgpack:"arg,omitempty"`
}

type wireLookupResult struct {
	Kind  uint8      `msgpack:"kind"`
	Value *wireValue `msgpack:"value,omitempty"`
}

type wireFutureResults struct {
	Version uint32                    `msgpack:"version"`
	Results map[uint32]wireCallResult `msgpack:"results"`
}

type wireProgressPayload struct {
	Variant        uint8       `msgpack:"variant"`
	Version        uint32      `msgpack:"version"`
	ScriptName     string      `msgpack:"script_name,omitempty"`
	IsRepl         bool        `msgpack:"is_repl,omitempty"`
	IsOSFunction   bool        `msgpack:"is_os_function,omitempty"`
	IsMethodCall   bool        `msgpack:"is_method_call,omitempty"`
	FunctionName   string      `msgpack:"function_name,omitempty"`
	Args           []wireValue `msgpack:"args,omitempty"`
	Kwargs         []wirePair  `msgpack:"kwargs,omitempty"`
	CallID         uint32      `msgpack:"call_id,omitempty"`
	VariableName   string      `msgpack:"variable_name,omitempty"`
	PendingCallIDs []uint32    `msgpack:"pending_call_ids,omitempty"`
	Output         *wireValue  `msgpack:"output,omitempty"`
}

func marshalWire(value any) ([]byte, error) {
	return msgpack.Marshal(value)
}

func unmarshalWire(data []byte, target any) error {
	return msgpack.Unmarshal(data, target)
}

func newWireCompileOptions(opts CompileOptions) wireCompileOptions {
	return wireCompileOptions{
		Version:        wireVersion,
		ScriptName:     optionalString(opts.ScriptName),
		Inputs:         opts.Inputs,
		TypeCheck:      opts.TypeCheck,
		TypeCheckStubs: optionalString(opts.TypeCheckStubs),
	}
}

func newWireStartOptions(opts StartOptions) (wireStartOptions, error) {
	inputs, err := wireValueMapFromPublic(opts.Inputs)
	if err != nil {
		return wireStartOptions{}, err
	}
	return wireStartOptions{
		Version: wireVersion,
		Inputs:  inputs,
		Limits:  newWireResourceLimits(opts.Limits),
	}, nil
}

func newWireReplOptions(opts ReplOptions) wireReplOptions {
	return wireReplOptions{
		Version:    wireVersion,
		ScriptName: optionalString(opts.ScriptName),
		Limits:     newWireResourceLimits(opts.Limits),
	}
}

func newWireFeedOptions(opts FeedStartOptions) (wireFeedOptions, error) {
	inputs, err := wireValueMapFromPublic(opts.Inputs)
	if err != nil {
		return wireFeedOptions{}, err
	}
	return wireFeedOptions{
		Version: wireVersion,
		Inputs:  inputs,
	}, nil
}

func newWireCallResult(result Result) (wireCallResult, error) {
	switch {
	case result.wirePending():
		return wireCallResult{Kind: wireCallResultPending}, nil
	case result.wireException() != nil:
		exception := result.wireException()
		return wireCallResult{
			Kind: wireCallResultException,
			Type: exception.Type,
			Arg:  exception.Arg,
		}, nil
	default:
		value, err := wireValueFromPublic(result.wireValue())
		if err != nil {
			return wireCallResult{}, err
		}
		return wireCallResult{
			Kind:  wireCallResultReturn,
			Value: &value,
		}, nil
	}
}

func (payload callResultPayload) toWire() (wireCallResult, error) {
	switch payload.Kind {
	case "return":
		wireValue, err := wireValueFromPublic(payload.Value)
		if err != nil {
			return wireCallResult{}, err
		}
		return wireCallResult{
			Kind:  wireCallResultReturn,
			Value: &wireValue,
		}, nil
	case "exception":
		return wireCallResult{
			Kind: wireCallResultException,
			Type: payload.Type,
			Arg:  payload.Arg,
		}, nil
	case "pending":
		return wireCallResult{Kind: wireCallResultPending}, nil
	default:
		return wireCallResult{}, fmt.Errorf("unknown call result kind %q", payload.Kind)
	}
}

func newWireLookupResult(value Value) (wireLookupResult, error) {
	wireValue, err := wireValueFromPublic(value)
	if err != nil {
		return wireLookupResult{}, err
	}
	return wireLookupResult{
		Kind:  wireLookupResultValue,
		Value: &wireValue,
	}, nil
}

func (payload lookupResultPayload) toWire() (wireLookupResult, error) {
	switch payload.Kind {
	case "value":
		return newWireLookupResult(payload.Value)
	case "undefined":
		return wireLookupResult{Kind: wireLookupResultUndefined}, nil
	default:
		return wireLookupResult{}, fmt.Errorf("unknown lookup result kind %q", payload.Kind)
	}
}

func newWireFutureResults(results map[uint32]Result) (wireFutureResults, error) {
	wireResults := make(map[uint32]wireCallResult, len(results))
	for callID, result := range results {
		wireResult, err := newWireCallResult(result)
		if err != nil {
			return wireFutureResults{}, err
		}
		wireResults[callID] = wireResult
	}
	return wireFutureResults{
		Version: wireVersion,
		Results: wireResults,
	}, nil
}

func newWireResourceLimits(limits *ResourceLimits) *wireResourceLimits {
	if limits == nil {
		return nil
	}
	payload := &wireResourceLimits{
		Version:           wireVersion,
		MaxAllocations:    optionalInt(limits.MaxAllocations),
		MaxMemory:         optionalInt(limits.MaxMemory),
		GCInterval:        optionalInt(limits.GCInterval),
		MaxRecursionDepth: optionalInt(limits.MaxRecursionDepth),
	}
	if limits.MaxDuration > 0 {
		seconds := limits.MaxDuration.Seconds()
		payload.MaxDurationSecs = &seconds
	}
	return payload
}

func wireValueMapFromPublic(values map[string]Value) (map[string]wireValue, error) {
	if len(values) == 0 {
		return nil, nil
	}
	wireValues := make(map[string]wireValue, len(values))
	for key, value := range values {
		wireValue, err := wireValueFromPublic(value)
		if err != nil {
			return nil, err
		}
		wireValues[key] = wireValue
	}
	return wireValues, nil
}

func wirePairsFromDict(dict Dict) ([]wirePair, error) {
	if len(dict) == 0 {
		return nil, nil
	}
	pairs := make([]wirePair, 0, len(dict))
	for _, item := range dict {
		key, err := wireValueFromPublic(item.Key)
		if err != nil {
			return nil, err
		}
		value, err := wireValueFromPublic(item.Value)
		if err != nil {
			return nil, err
		}
		pairs = append(pairs, wirePair{Key: key, Value: value})
	}
	return pairs, nil
}

func dictFromWirePairs(pairs []wirePair) (Dict, error) {
	if len(pairs) == 0 {
		return nil, nil
	}
	dict := make(Dict, 0, len(pairs))
	for _, pair := range pairs {
		key, err := pair.Key.toPublic()
		if err != nil {
			return nil, err
		}
		value, err := pair.Value.toPublic()
		if err != nil {
			return nil, err
		}
		dict = append(dict, Pair{Key: key, Value: value})
	}
	return dict, nil
}

func wireValuesFromPublic(values []Value) ([]wireValue, error) {
	if len(values) == 0 {
		return nil, nil
	}
	items := make([]wireValue, 0, len(values))
	for _, value := range values {
		wireValue, err := wireValueFromPublic(value)
		if err != nil {
			return nil, err
		}
		items = append(items, wireValue)
	}
	return items, nil
}

func valuesFromWire(items []wireValue) ([]Value, error) {
	if len(items) == 0 {
		return nil, nil
	}
	values := make([]Value, 0, len(items))
	for _, item := range items {
		value, err := item.toPublic()
		if err != nil {
			return nil, err
		}
		values = append(values, value)
	}
	return values, nil
}

func wireValueFromPublic(value Value) (wireValue, error) {
	switch value.Kind() {
	case valueKindNone:
		return wireValue{Kind: wireValueNone}, nil
	case valueKindEllipsis:
		return wireValue{Kind: wireValueEllipsis}, nil
	case valueKindBool:
		return wireValue{Kind: wireValueBool, Bool: value.data.(bool)}, nil
	case valueKindInt:
		return wireValue{Kind: wireValueInt, Int: value.data.(int64)}, nil
	case valueKindBigInt:
		return wireValue{Kind: wireValueBigInt, BigInt: value.data.(*big.Int).String()}, nil
	case valueKindFloat:
		return wireValue{Kind: wireValueFloat, Float: value.data.(float64)}, nil
	case valueKindString:
		return wireValue{Kind: wireValueString, String: value.data.(string)}, nil
	case valueKindBytes:
		return wireValue{Kind: wireValueBytes, Bytes: append([]byte(nil), value.data.([]byte)...)}, nil
	case valueKindList:
		items, err := wireValuesFromPublic(value.data.([]Value))
		if err != nil {
			return wireValue{}, err
		}
		return wireValue{Kind: wireValueList, Items: items}, nil
	case valueKindTuple:
		items, err := wireValuesFromPublic([]Value(value.data.(Tuple)))
		if err != nil {
			return wireValue{}, err
		}
		return wireValue{Kind: wireValueTuple, Items: items}, nil
	case valueKindNamedTuple:
		namedTuple := value.data.(NamedTuple)
		values, err := wireValuesFromPublic(namedTuple.Values)
		if err != nil {
			return wireValue{}, err
		}
		return wireValue{
			Kind:       wireValueNamedTuple,
			TypeName:   namedTuple.TypeName,
			FieldNames: append([]string(nil), namedTuple.FieldNames...),
			Values:     values,
		}, nil
	case valueKindDict:
		pairs, err := wirePairsFromDict(value.data.(Dict))
		if err != nil {
			return wireValue{}, err
		}
		return wireValue{Kind: wireValueDict, Pairs: pairs}, nil
	case valueKindSet:
		items, err := wireValuesFromPublic([]Value(value.data.(Set)))
		if err != nil {
			return wireValue{}, err
		}
		return wireValue{Kind: wireValueSet, Items: items}, nil
	case valueKindFrozenSet:
		items, err := wireValuesFromPublic([]Value(value.data.(FrozenSet)))
		if err != nil {
			return wireValue{}, err
		}
		return wireValue{Kind: wireValueFrozenSet, Items: items}, nil
	case valueKindException:
		exception := value.data.(Exception)
		return wireValue{
			Kind:    wireValueException,
			ExcType: exception.Type,
			Arg:     exception.Arg,
		}, nil
	case valueKindPath:
		return wireValue{Kind: wireValuePath, String: string(value.data.(Path))}, nil
	case valueKindDataclass:
		dataclass := value.data.(Dataclass)
		attrs, err := wirePairsFromDict(dataclass.Attrs)
		if err != nil {
			return wireValue{}, err
		}
		return wireValue{
			Kind:       wireValueDataclass,
			Name:       dataclass.Name,
			TypeID:     dataclass.TypeID,
			FieldNames: append([]string(nil), dataclass.FieldNames...),
			Attrs:      attrs,
			Frozen:     dataclass.Frozen,
		}, nil
	case valueKindFunction:
		function := value.data.(Function)
		return wireValue{
			Kind:      wireValueFunction,
			Name:      function.Name,
			Docstring: function.Docstring,
		}, nil
	case valueKindRepr:
		return wireValue{Kind: wireValueRepr, String: value.data.(string)}, nil
	case valueKindCycle:
		return wireValue{Kind: wireValueCycle, Placeholder: value.data.(string)}, nil
	default:
		return wireValue{}, fmt.Errorf("unsupported value kind %q", value.Kind())
	}
}

func (value wireValue) toPublic() (Value, error) {
	switch value.Kind {
	case wireValueNone:
		return None(), nil
	case wireValueEllipsis:
		return Ellipsis(), nil
	case wireValueBool:
		return Bool(value.Bool), nil
	case wireValueInt:
		return Int(value.Int), nil
	case wireValueBigInt:
		bigValue, ok := new(big.Int).SetString(value.BigInt, 10)
		if !ok {
			return Value{}, fmt.Errorf("invalid bigint %q", value.BigInt)
		}
		return BigInt(bigValue), nil
	case wireValueFloat:
		return Float(value.Float), nil
	case wireValueString:
		return String(value.String), nil
	case wireValueBytes:
		return Bytes(value.Bytes), nil
	case wireValueList:
		items, err := valuesFromWire(value.Items)
		if err != nil {
			return Value{}, err
		}
		return List(items...), nil
	case wireValueTuple:
		items, err := valuesFromWire(value.Items)
		if err != nil {
			return Value{}, err
		}
		return Value{kind: valueKindTuple, data: Tuple(items)}, nil
	case wireValueNamedTuple:
		values, err := valuesFromWire(value.Values)
		if err != nil {
			return Value{}, err
		}
		return NamedTupleValue(NamedTuple{
			TypeName:   value.TypeName,
			FieldNames: append([]string(nil), value.FieldNames...),
			Values:     values,
		}), nil
	case wireValueDict:
		dict, err := dictFromWirePairs(value.Pairs)
		if err != nil {
			return Value{}, err
		}
		return DictValue(dict), nil
	case wireValueSet:
		items, err := valuesFromWire(value.Items)
		if err != nil {
			return Value{}, err
		}
		return SetValue(Set(items)), nil
	case wireValueFrozenSet:
		items, err := valuesFromWire(value.Items)
		if err != nil {
			return Value{}, err
		}
		return FrozenSetValue(FrozenSet(items)), nil
	case wireValueException:
		return ExceptionValue(Exception{Type: value.ExcType, Arg: value.Arg}), nil
	case wireValuePath:
		return PathValue(Path(value.String)), nil
	case wireValueDataclass:
		attrs, err := dictFromWirePairs(value.Attrs)
		if err != nil {
			return Value{}, err
		}
		return DataclassValue(Dataclass{
			Name:       value.Name,
			TypeID:     value.TypeID,
			FieldNames: append([]string(nil), value.FieldNames...),
			Attrs:      attrs,
			Frozen:     value.Frozen,
		}), nil
	case wireValueFunction:
		return FunctionValue(Function{Name: value.Name, Docstring: value.Docstring}), nil
	case wireValueRepr:
		return ReprValue(value.String), nil
	case wireValueCycle:
		return CycleValue(value.Placeholder), nil
	default:
		return Value{}, fmt.Errorf("unknown wire value kind %d", value.Kind)
	}
}
