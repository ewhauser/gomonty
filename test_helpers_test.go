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
