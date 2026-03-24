package monty

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"

	"github.com/ewhauser/gomonty/internal/ffi"
)

const wireVersion = 1

var (
	// ErrClosed reports that a handle-backed object no longer owns a native handle.
	ErrClosed = errors.New("monty handle is closed")
	// ErrConcurrentUse reports that the same handle-backed object is already being used.
	ErrConcurrentUse = errors.New("monty handle is already in use")
)

// Runner is the compiled runner entrypoint for Monty code.
type Runner struct {
	state *runnerState
}

// Repl is a stateful Monty REPL session.
type Repl struct {
	state *replState
}

// Progress is the low-level pause/resume execution interface.
type Progress interface {
	isProgress()
}

// Snapshot is a paused external function, method call, or OS call.
type Snapshot struct {
	progressBase
	FunctionName string
	Args         []Value
	Kwargs       Dict
	CallID       uint32
	IsOSFunction bool
	IsMethodCall bool
}

// NameLookupSnapshot is a paused unresolved-name lookup.
type NameLookupSnapshot struct {
	progressBase
	VariableName string
}

// FutureSnapshot is a paused future-resolution state.
type FutureSnapshot struct {
	progressBase
	pendingCallIDs []uint32
}

// Complete is a finished execution result.
type Complete struct {
	Output     Value
	ScriptName string
	IsRepl     bool
}

type runnerState struct {
	mu     sync.Mutex
	handle *ffi.Runner
}

type replState struct {
	mu       sync.Mutex
	handle   *ffi.Repl
	inFlight bool
}

type progressState struct {
	mu     sync.Mutex
	handle *ffi.Progress
	owner  *replState
}

type progressBase struct {
	state      *progressState
	scriptName string
	isRepl     bool
}

type compileOptionsPayload struct {
	Version        uint32   `json:"version"`
	ScriptName     *string  `json:"script_name,omitempty"`
	Inputs         []string `json:"inputs,omitempty"`
	TypeCheck      bool     `json:"type_check,omitempty"`
	TypeCheckStubs *string  `json:"type_check_stubs,omitempty"`
}

type resourceLimitsPayload struct {
	Version           uint32   `json:"version"`
	MaxAllocations    *int     `json:"max_allocations,omitempty"`
	MaxDurationSecs   *float64 `json:"max_duration_secs,omitempty"`
	MaxMemory         *int     `json:"max_memory,omitempty"`
	GCInterval        *int     `json:"gc_interval,omitempty"`
	MaxRecursionDepth *int     `json:"max_recursion_depth,omitempty"`
}

type startOptionsPayload struct {
	Version uint32                 `json:"version"`
	Inputs  map[string]Value       `json:"inputs,omitempty"`
	Limits  *resourceLimitsPayload `json:"limits,omitempty"`
}

type replOptionsPayload struct {
	Version    uint32                 `json:"version"`
	ScriptName *string                `json:"script_name,omitempty"`
	Limits     *resourceLimitsPayload `json:"limits,omitempty"`
}

type feedOptionsPayload struct {
	Version uint32           `json:"version"`
	Inputs  map[string]Value `json:"inputs,omitempty"`
}

type callResultPayload struct {
	Kind  string  `json:"kind"`
	Value Value   `json:"value,omitempty"`
	Type  string  `json:"exc_type,omitempty"`
	Arg   *string `json:"arg,omitempty"`
}

type lookupResultPayload struct {
	Kind  string `json:"kind"`
	Value Value  `json:"value,omitempty"`
}

type futureResultsPayload struct {
	Version uint32                       `json:"version"`
	Results map[uint32]callResultPayload `json:"results"`
}

type progressDescription struct {
	Variant        string   `json:"variant"`
	Version        uint32   `json:"version"`
	ScriptName     string   `json:"script_name"`
	IsRepl         bool     `json:"is_repl"`
	IsOSFunction   bool     `json:"is_os_function"`
	IsMethodCall   bool     `json:"is_method_call"`
	FunctionName   string   `json:"function_name"`
	Args           []Value  `json:"args"`
	Kwargs         Dict     `json:"kwargs"`
	CallID         uint32   `json:"call_id"`
	VariableName   string   `json:"variable_name"`
	PendingCallIDs []uint32 `json:"pending_call_ids"`
	Output         Value    `json:"output"`
}

// New compiles Monty code into a reusable runner.
func New(code string, opts CompileOptions) (*Runner, error) {
	payload, err := marshalJSON(compileOptionsPayload{
		Version:        wireVersion,
		ScriptName:     optionalString(opts.ScriptName),
		Inputs:         opts.Inputs,
		TypeCheck:      opts.TypeCheck,
		TypeCheckStubs: optionalString(opts.TypeCheckStubs),
	})
	if err != nil {
		return nil, err
	}

	result := ffi.NewRunner([]byte(code), payload)
	if result.Error != nil {
		return nil, newError(result.Error)
	}
	return &Runner{
		state: &runnerState{handle: result.Runner},
	}, nil
}

// LoadRunner restores a serialized runner.
func LoadRunner(data []byte) (*Runner, error) {
	result := ffi.LoadRunner(data)
	if result.Error != nil {
		return nil, newError(result.Error)
	}
	return &Runner{
		state: &runnerState{handle: result.Runner},
	}, nil
}

// LoadSnapshot restores a serialized non-REPL or REPL snapshot without an owner REPL.
func LoadSnapshot(data []byte) (Progress, error) {
	result := ffi.LoadProgress(data)
	if result.Error != nil {
		return nil, newError(result.Error)
	}
	return progressFromHandle(result.Progress, nil)
}

// LoadReplSnapshot restores a serialized REPL snapshot and also returns a base
// REPL handle that can be used to abandon the in-flight snippet.
func LoadReplSnapshot(data []byte) (Progress, *Repl, error) {
	owner := &Repl{state: &replState{}}

	primary := ffi.LoadProgress(data)
	if primary.Error != nil {
		return nil, nil, newError(primary.Error)
	}

	backup := ffi.LoadProgress(data)
	if backup.Error != nil {
		primary.Progress.Close()
		return nil, nil, newError(backup.Error)
	}

	replResult := backup.Progress.TakeRepl()
	backup.Progress.Close()
	if replResult.Error != nil {
		primary.Progress.Close()
		return nil, nil, newError(replResult.Error)
	}
	owner.state.restore(replResult.Repl)

	progress, err := progressFromHandle(primary.Progress, owner.state)
	if err != nil {
		return nil, nil, err
	}
	return progress, owner, nil
}

// NewRepl constructs an empty REPL session.
func NewRepl(opts ReplOptions) (*Repl, error) {
	payload, err := marshalJSON(replOptionsPayload{
		Version:    wireVersion,
		ScriptName: optionalString(opts.ScriptName),
		Limits:     encodeResourceLimits(opts.Limits),
	})
	if err != nil {
		return nil, err
	}

	result := ffi.NewRepl(payload)
	if result.Error != nil {
		return nil, newError(result.Error)
	}
	return &Repl{
		state: &replState{handle: result.Repl},
	}, nil
}

// Dump serializes the runner.
func (r *Runner) Dump() ([]byte, error) {
	handle, release, err := r.state.borrow()
	if err != nil {
		return nil, err
	}
	defer release()

	data, ffiErr := handle.Dump()
	if ffiErr != nil {
		return nil, newError(ffiErr)
	}
	return data, nil
}

// TypeCheck runs static type checking on the compiled runner.
func (r *Runner) TypeCheck(prefix string) error {
	handle, release, err := r.state.borrow()
	if err != nil {
		return err
	}
	defer release()

	if ffiErr := handle.TypeCheck([]byte(prefix)); ffiErr != nil {
		return newError(ffiErr)
	}
	return nil
}

// Start begins low-level runner execution.
func (r *Runner) Start(ctx context.Context, opts StartOptions) (Progress, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	handle, release, err := r.state.borrow()
	if err != nil {
		return nil, err
	}
	defer release()

	payload, err := marshalJSON(startOptionsPayload{
		Version: wireVersion,
		Inputs:  cloneValueMap(opts.Inputs),
		Limits:  encodeResourceLimits(opts.Limits),
	})
	if err != nil {
		return nil, err
	}

	result := handle.Start(payload)
	return consumeOpResult(result, nil, nil)
}

// Run executes the runner through the high-level host callback loop.
func (r *Runner) Run(ctx context.Context, opts RunOptions) (Value, error) {
	progress, err := r.Start(ctx, StartOptions{
		Inputs: opts.Inputs,
		Limits: opts.Limits,
	})
	if err != nil {
		return Value{}, err
	}
	return dispatchLoop(ctx, progress, dispatchConfig{
		functions: opts.Functions,
		os:        opts.OS,
		print:     opts.Print,
	})
}

// Dump serializes the REPL session.
func (r *Repl) Dump() ([]byte, error) {
	handle, release, err := r.state.borrow()
	if err != nil {
		return nil, err
	}
	defer release()

	data, ffiErr := handle.Dump()
	if ffiErr != nil {
		return nil, newError(ffiErr)
	}
	return data, nil
}

// FeedStart begins low-level REPL snippet execution.
func (r *Repl) FeedStart(ctx context.Context, code string, opts FeedStartOptions) (Progress, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	handle, err := r.state.takeForExecution()
	if err != nil {
		return nil, err
	}

	payload, err := marshalJSON(feedOptionsPayload{
		Version: wireVersion,
		Inputs:  cloneValueMap(opts.Inputs),
	})
	if err != nil {
		r.state.restore(handle)
		return nil, err
	}

	result := handle.FeedStart([]byte(code), payload)
	handle.Close()
	progress, consumeErr := consumeOpResult(result, r.state, nil)
	if consumeErr != nil {
		return nil, consumeErr
	}
	return progress, nil
}

// FeedRun executes a REPL snippet through the high-level host callback loop.
func (r *Repl) FeedRun(ctx context.Context, code string, opts FeedOptions) (Value, error) {
	progress, err := r.FeedStart(ctx, code, FeedStartOptions{Inputs: opts.Inputs})
	if err != nil {
		return Value{}, err
	}
	return dispatchLoop(ctx, progress, dispatchConfig{
		functions: opts.Functions,
		os:        opts.OS,
		print:     opts.Print,
	})
}

// Dump serializes the current snapshot.
func (s *Snapshot) Dump() ([]byte, error) {
	return s.progressBase.dump()
}

// Dump serializes the current name-lookup snapshot.
func (s *NameLookupSnapshot) Dump() ([]byte, error) {
	return s.progressBase.dump()
}

// Dump serializes the current future snapshot.
func (s *FutureSnapshot) Dump() ([]byte, error) {
	return s.progressBase.dump()
}

// ResumeReturn resumes a function or OS snapshot with a return value.
func (s *Snapshot) ResumeReturn(ctx context.Context, value Value) (Progress, error) {
	payload, err := marshalJSON(callResultPayload{
		Kind:  "return",
		Value: value,
	})
	if err != nil {
		return nil, err
	}
	return s.progressBase.resumeCall(ctx, payload, nil)
}

// ResumeException resumes a function or OS snapshot by raising an exception.
func (s *Snapshot) ResumeException(ctx context.Context, exception Exception) (Progress, error) {
	payload, err := marshalJSON(callResultPayload{
		Kind: "exception",
		Type: exception.Type,
		Arg:  exception.Arg,
	})
	if err != nil {
		return nil, err
	}
	return s.progressBase.resumeCall(ctx, payload, nil)
}

// ResumePending resumes a function or OS snapshot with a pending future.
func (s *Snapshot) ResumePending(ctx context.Context) (Progress, error) {
	payload, err := marshalJSON(callResultPayload{Kind: "pending"})
	if err != nil {
		return nil, err
	}
	return s.progressBase.resumeCall(ctx, payload, nil)
}

// ResumeValue resumes a name lookup with a resolved value.
func (s *NameLookupSnapshot) ResumeValue(ctx context.Context, value Value) (Progress, error) {
	payload, err := marshalJSON(lookupResultPayload{
		Kind:  "value",
		Value: value,
	})
	if err != nil {
		return nil, err
	}
	return s.progressBase.resumeLookup(ctx, payload, nil)
}

// ResumeUndefined resumes a name lookup as undefined.
func (s *NameLookupSnapshot) ResumeUndefined(ctx context.Context) (Progress, error) {
	payload, err := marshalJSON(lookupResultPayload{Kind: "undefined"})
	if err != nil {
		return nil, err
	}
	return s.progressBase.resumeLookup(ctx, payload, nil)
}

// PendingCallIDs returns the unresolved call IDs for the future snapshot.
func (s *FutureSnapshot) PendingCallIDs() []uint32 {
	return append([]uint32(nil), s.pendingCallIDs...)
}

// ResumeResults resumes a future snapshot with zero or more resolved call IDs.
func (s *FutureSnapshot) ResumeResults(ctx context.Context, results map[uint32]Result) (Progress, error) {
	wireResults := make(map[uint32]callResultPayload, len(results))
	for callID, result := range results {
		switch {
		case result.wirePending():
			wireResults[callID] = callResultPayload{Kind: "pending"}
		case result.wireException() != nil:
			exception := result.wireException()
			wireResults[callID] = callResultPayload{
				Kind: "exception",
				Type: exception.Type,
				Arg:  exception.Arg,
			}
		default:
			wireResults[callID] = callResultPayload{
				Kind:  "return",
				Value: result.wireValue(),
			}
		}
	}
	payload, err := marshalJSON(futureResultsPayload{
		Version: wireVersion,
		Results: wireResults,
	})
	if err != nil {
		return nil, err
	}
	return s.progressBase.resumeFutures(ctx, payload, nil)
}

func (s *Snapshot) isProgress()           {}
func (s *NameLookupSnapshot) isProgress() {}
func (s *FutureSnapshot) isProgress()     {}
func (s *Complete) isProgress()           {}

func (s *Snapshot) restoreOwner() error {
	return s.progressBase.restoreOwner()
}

func (s *NameLookupSnapshot) restoreOwner() error {
	return s.progressBase.restoreOwner()
}

func (s *FutureSnapshot) restoreOwner() error {
	return s.progressBase.restoreOwner()
}

func (b *progressBase) dump() ([]byte, error) {
	handle, release, err := b.state.borrow()
	if err != nil {
		return nil, err
	}
	defer release()

	bytes, ffiErr := handle.Dump()
	if ffiErr != nil {
		return nil, newError(ffiErr)
	}
	return bytes, nil
}

func (b *progressBase) resumeCall(ctx context.Context, payload []byte, print PrintCallback) (Progress, error) {
	if err := ctx.Err(); err != nil {
		if restoreErr := b.restoreOwner(); restoreErr != nil {
			return nil, errors.Join(err, restoreErr)
		}
		return nil, err
	}

	handle, owner, err := b.state.take()
	if err != nil {
		return nil, err
	}
	defer handle.Close()

	return consumeOpResult(handle.ResumeCall(payload), owner, print)
}

func (b *progressBase) resumeLookup(ctx context.Context, payload []byte, print PrintCallback) (Progress, error) {
	if err := ctx.Err(); err != nil {
		if restoreErr := b.restoreOwner(); restoreErr != nil {
			return nil, errors.Join(err, restoreErr)
		}
		return nil, err
	}

	handle, owner, err := b.state.take()
	if err != nil {
		return nil, err
	}
	defer handle.Close()

	return consumeOpResult(handle.ResumeLookup(payload), owner, print)
}

func (b *progressBase) resumeFutures(ctx context.Context, payload []byte, print PrintCallback) (Progress, error) {
	if err := ctx.Err(); err != nil {
		if restoreErr := b.restoreOwner(); restoreErr != nil {
			return nil, errors.Join(err, restoreErr)
		}
		return nil, err
	}

	handle, owner, err := b.state.take()
	if err != nil {
		return nil, err
	}
	defer handle.Close()

	return consumeOpResult(handle.ResumeFutures(payload), owner, print)
}

func (b *progressBase) restoreOwner() error {
	handle, owner, err := b.state.take()
	if err != nil {
		return err
	}
	defer handle.Close()

	if owner == nil {
		return nil
	}
	result := handle.TakeRepl()
	if result.Error != nil {
		owner.clear()
		return newError(result.Error)
	}
	owner.restore(result.Repl)
	return nil
}

func (s *runnerState) borrow() (*ffi.Runner, func(), error) {
	if s == nil || !s.mu.TryLock() {
		return nil, nil, ErrConcurrentUse
	}
	if s.handle == nil {
		s.mu.Unlock()
		return nil, nil, ErrClosed
	}
	return s.handle, s.mu.Unlock, nil
}

func (s *replState) borrow() (*ffi.Repl, func(), error) {
	if s == nil || !s.mu.TryLock() {
		return nil, nil, ErrConcurrentUse
	}
	if s.handle == nil {
		s.mu.Unlock()
		if s.inFlight {
			return nil, nil, ErrConcurrentUse
		}
		return nil, nil, ErrClosed
	}
	return s.handle, s.mu.Unlock, nil
}

func (s *replState) takeForExecution() (*ffi.Repl, error) {
	if s == nil || !s.mu.TryLock() {
		return nil, ErrConcurrentUse
	}
	defer s.mu.Unlock()

	if s.handle == nil {
		if s.inFlight {
			return nil, ErrConcurrentUse
		}
		return nil, ErrClosed
	}
	handle := s.handle
	s.handle = nil
	s.inFlight = true
	return handle, nil
}

func (s *replState) restore(handle *ffi.Repl) {
	s.mu.Lock()
	old := s.handle
	s.handle = handle
	s.inFlight = false
	s.mu.Unlock()
	if old != nil {
		old.Close()
	}
}

func (s *replState) clear() {
	s.mu.Lock()
	old := s.handle
	s.handle = nil
	s.inFlight = false
	s.mu.Unlock()
	if old != nil {
		old.Close()
	}
}

func (s *progressState) borrow() (*ffi.Progress, func(), error) {
	if s == nil || !s.mu.TryLock() {
		return nil, nil, ErrConcurrentUse
	}
	if s.handle == nil {
		s.mu.Unlock()
		return nil, nil, ErrClosed
	}
	return s.handle, s.mu.Unlock, nil
}

func (s *progressState) take() (*ffi.Progress, *replState, error) {
	if s == nil || !s.mu.TryLock() {
		return nil, nil, ErrConcurrentUse
	}
	defer s.mu.Unlock()

	if s.handle == nil {
		return nil, nil, ErrClosed
	}
	handle := s.handle
	s.handle = nil
	return handle, s.owner, nil
}

func consumeOpResult(result ffi.OpResult, owner *replState, print PrintCallback) (Progress, error) {
	if print != nil && result.Prints != "" {
		print("stdout", result.Prints)
	}

	if result.Error != nil {
		if owner != nil {
			if result.Repl != nil {
				owner.restore(result.Repl)
			} else {
				owner.clear()
			}
		} else if result.Repl != nil {
			result.Repl.Close()
		}
		if result.Progress != nil {
			result.Progress.Close()
		}
		return nil, newError(result.Error)
	}

	if result.Progress == nil {
		if owner != nil {
			owner.clear()
		}
		if result.Repl != nil {
			result.Repl.Close()
		}
		return nil, fmt.Errorf("monty ffi returned no progress handle")
	}

	return progressFromHandle(result.Progress, owner)
}

func progressFromHandle(handle *ffi.Progress, owner *replState) (Progress, error) {
	bytes, ffiErr := handle.Describe()
	if ffiErr != nil {
		handle.Close()
		if owner != nil {
			owner.clear()
		}
		return nil, newError(ffiErr)
	}

	var desc progressDescription
	if err := json.Unmarshal(bytes, &desc); err != nil {
		handle.Close()
		if owner != nil {
			owner.clear()
		}
		return nil, fmt.Errorf("invalid progress payload: %w", err)
	}

	state := &progressState{
		handle: handle,
		owner:  owner,
	}
	base := progressBase{
		state:      state,
		scriptName: desc.ScriptName,
		isRepl:     desc.IsRepl,
	}

	switch desc.Variant {
	case "function_call":
		return &Snapshot{
			progressBase: base,
			FunctionName: desc.FunctionName,
			Args:         desc.Args,
			Kwargs:       desc.Kwargs,
			CallID:       desc.CallID,
			IsOSFunction: desc.IsOSFunction,
			IsMethodCall: desc.IsMethodCall,
		}, nil
	case "name_lookup":
		return &NameLookupSnapshot{
			progressBase: base,
			VariableName: desc.VariableName,
		}, nil
	case "future":
		return &FutureSnapshot{
			progressBase:   base,
			pendingCallIDs: append([]uint32(nil), desc.PendingCallIDs...),
		}, nil
	case "complete":
		if owner != nil {
			result := handle.TakeRepl()
			handle.Close()
			state.handle = nil
			if result.Error != nil {
				owner.clear()
				return nil, newError(result.Error)
			}
			owner.restore(result.Repl)
		} else {
			handle.Close()
			state.handle = nil
		}
		return &Complete{
			Output:     desc.Output,
			ScriptName: desc.ScriptName,
			IsRepl:     desc.IsRepl,
		}, nil
	default:
		handle.Close()
		if owner != nil {
			owner.clear()
		}
		return nil, fmt.Errorf("unknown progress variant %q", desc.Variant)
	}
}

func marshalJSON(value any) ([]byte, error) {
	return json.Marshal(value)
}

func cloneValueMap(values map[string]Value) map[string]Value {
	if len(values) == 0 {
		return nil
	}
	cloned := make(map[string]Value, len(values))
	for key, value := range values {
		cloned[key] = value
	}
	return cloned
}

func optionalString(value string) *string {
	if value == "" {
		return nil
	}
	return &value
}

func optionalInt(value int) *int {
	if value == 0 {
		return nil
	}
	return &value
}

func encodeResourceLimits(limits *ResourceLimits) *resourceLimitsPayload {
	if limits == nil {
		return nil
	}
	payload := &resourceLimitsPayload{
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
