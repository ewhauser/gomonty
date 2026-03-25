package monty

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/ewhauser/gomonty/internal/ffi"
)

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

// New compiles Monty code into a reusable runner.
func New(code string, opts CompileOptions) (*Runner, error) {
	payload, err := marshalWire(newWireCompileOptions(opts))
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
	return progressFromResult(result.Progress, result.ProgressPayload, nil)
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

	progress, err := progressFromResult(primary.Progress, primary.ProgressPayload, owner.state)
	if err != nil {
		return nil, nil, err
	}
	return progress, owner, nil
}

// NewRepl constructs an empty REPL session.
func NewRepl(opts ReplOptions) (*Repl, error) {
	payload, err := marshalWire(newWireReplOptions(opts))
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
	return r.start(ctx, opts, nil)
}

func (r *Runner) start(ctx context.Context, opts StartOptions, print PrintCallback) (Progress, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	handle, release, err := r.state.borrow()
	if err != nil {
		return nil, err
	}
	defer release()

	wireOptions, err := newWireStartOptions(opts)
	if err != nil {
		return nil, err
	}

	payload, err := marshalWire(wireOptions)
	if err != nil {
		return nil, err
	}

	result := handle.Start(payload)
	return consumeOpResult(result, nil, print)
}

// Run executes the runner through the high-level host callback loop.
func (r *Runner) Run(ctx context.Context, opts RunOptions) (Value, error) {
	progress, err := r.start(ctx, StartOptions{
		Inputs: opts.Inputs,
		Limits: opts.Limits,
	}, opts.Print)
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
	return r.feedStart(ctx, code, opts, nil)
}

func (r *Repl) feedStart(ctx context.Context, code string, opts FeedStartOptions, print PrintCallback) (Progress, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	handle, err := r.state.takeForExecution()
	if err != nil {
		return nil, err
	}

	wireOptions, err := newWireFeedOptions(opts)
	if err != nil {
		return nil, err
	}

	payload, err := marshalWire(wireOptions)
	if err != nil {
		r.state.restore(handle)
		return nil, err
	}

	result := handle.FeedStart([]byte(code), payload)
	handle.Close()
	progress, consumeErr := consumeOpResult(result, r.state, print)
	if consumeErr != nil {
		return nil, consumeErr
	}
	return progress, nil
}

// FeedRun executes a REPL snippet through the high-level host callback loop.
func (r *Repl) FeedRun(ctx context.Context, code string, opts FeedOptions) (Value, error) {
	progress, err := r.feedStart(ctx, code, FeedStartOptions{Inputs: opts.Inputs}, opts.Print)
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
	wireResult, err := newWireCallResult(Return(value))
	if err != nil {
		return nil, err
	}

	payload, err := marshalWire(wireResult)
	if err != nil {
		return nil, err
	}
	return s.progressBase.resumeCall(ctx, payload, nil)
}

// ResumeException resumes a function or OS snapshot by raising an exception.
func (s *Snapshot) ResumeException(ctx context.Context, exception Exception) (Progress, error) {
	wireResult, err := newWireCallResult(Raise(exception))
	if err != nil {
		return nil, err
	}

	payload, err := marshalWire(wireResult)
	if err != nil {
		return nil, err
	}
	return s.progressBase.resumeCall(ctx, payload, nil)
}

// ResumePending resumes a function or OS snapshot with a pending future.
func (s *Snapshot) ResumePending(ctx context.Context) (Progress, error) {
	payload, err := marshalWire(wireCallResult{Kind: wireCallResultPending})
	if err != nil {
		return nil, err
	}
	return s.progressBase.resumeCall(ctx, payload, nil)
}

// ResumeValue resumes a name lookup with a resolved value.
func (s *NameLookupSnapshot) ResumeValue(ctx context.Context, value Value) (Progress, error) {
	wireResult, err := newWireLookupResult(value)
	if err != nil {
		return nil, err
	}

	payload, err := marshalWire(wireResult)
	if err != nil {
		return nil, err
	}
	return s.progressBase.resumeLookup(ctx, payload, nil)
}

// ResumeUndefined resumes a name lookup as undefined.
func (s *NameLookupSnapshot) ResumeUndefined(ctx context.Context) (Progress, error) {
	payload, err := marshalWire(wireLookupResult{Kind: wireLookupResultUndefined})
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
	wireResults, err := newWireFutureResults(results)
	if err != nil {
		return nil, err
	}

	payload, err := marshalWire(wireResults)
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

	return progressFromResult(result.Progress, result.ProgressPayload, owner)
}

func progressFromResult(handle *ffi.Progress, payload []byte, owner *replState) (Progress, error) {
	if len(payload) == 0 {
		handle.Close()
		if owner != nil {
			owner.clear()
		}
		return nil, fmt.Errorf("monty ffi returned no progress payload")
	}

	var desc wireProgressPayload
	if err := unmarshalWire(payload, &desc); err != nil {
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
	case wireProgressFunctionCall:
		args, err := valuesFromWire(desc.Args)
		if err != nil {
			handle.Close()
			if owner != nil {
				owner.clear()
			}
			return nil, err
		}
		kwargs, err := dictFromWirePairs(desc.Kwargs)
		if err != nil {
			handle.Close()
			if owner != nil {
				owner.clear()
			}
			return nil, err
		}
		return &Snapshot{
			progressBase: base,
			FunctionName: desc.FunctionName,
			Args:         args,
			Kwargs:       kwargs,
			CallID:       desc.CallID,
			IsOSFunction: desc.IsOSFunction,
			IsMethodCall: desc.IsMethodCall,
		}, nil
	case wireProgressNameLookup:
		return &NameLookupSnapshot{
			progressBase: base,
			VariableName: desc.VariableName,
		}, nil
	case wireProgressFuture:
		return &FutureSnapshot{
			progressBase:   base,
			pendingCallIDs: append([]uint32(nil), desc.PendingCallIDs...),
		}, nil
	case wireProgressComplete:
		if desc.Output == nil {
			handle.Close()
			if owner != nil {
				owner.clear()
			}
			return nil, fmt.Errorf("complete progress payload is missing output")
		}
		output, err := desc.Output.toPublic()
		if err != nil {
			handle.Close()
			if owner != nil {
				owner.clear()
			}
			return nil, err
		}
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
			Output:     output,
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
