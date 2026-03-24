package monty

func closeTestRunner(runner *Runner) {
	if runner == nil || runner.state == nil {
		return
	}

	runner.state.mu.Lock()
	handle := runner.state.handle
	runner.state.handle = nil
	runner.state.mu.Unlock()

	if handle != nil {
		handle.Close()
	}
}

func closeTestProgress(progress Progress) {
	switch current := progress.(type) {
	case *Snapshot:
		closeTestProgressState(current.progressBase.state)
	case *NameLookupSnapshot:
		closeTestProgressState(current.progressBase.state)
	case *FutureSnapshot:
		closeTestProgressState(current.progressBase.state)
	case *Complete, nil:
		return
	default:
		panic("unexpected progress type")
	}
}

func closeTestProgressState(state *progressState) {
	if state == nil {
		return
	}

	state.mu.Lock()
	handle := state.handle
	state.handle = nil
	owner := state.owner
	state.owner = nil
	state.mu.Unlock()

	if handle != nil {
		handle.Close()
	}
	if owner != nil {
		owner.clear()
	}
}
