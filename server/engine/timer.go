package engine

func (e *Engine) startIdleTimer() {
	e.stopIdleTimer()
	e.idleTimer = e.clock.AfterFunc(e.config.IdleCompletionDelay, func() {
		// Check if engine is stopped before sending event
		e.mu.RLock()
		stopped := e.stopped
		mainCtx := e.mainCtx
		e.mu.RUnlock()

		if stopped || mainCtx == nil {
			return
		}

		select {
		case e.eventChan <- Event{Type: EventIdleTimeout}:
		case <-mainCtx.Done():
		}
	})
}

func (e *Engine) stopIdleTimer() {
	if e.idleTimer != nil {
		e.idleTimer.Stop()
		e.idleTimer = nil
	}
}

func (e *Engine) resetIdleTimer() {
	e.stopIdleTimer()
	e.startIdleTimer()
}

func (e *Engine) startTextChangeTimer() {
	e.stopTextChangeTimer()
	e.textChangeTimer = e.clock.AfterFunc(e.config.TextChangeDebounce, func() {
		// Check if engine is stopped before sending event
		e.mu.RLock()
		stopped := e.stopped
		mainCtx := e.mainCtx
		e.mu.RUnlock()

		if stopped || mainCtx == nil {
			return
		}

		select {
		case e.eventChan <- Event{Type: EventTextChangeTimeout, Data: nil}:
		case <-mainCtx.Done():
		}
	})
}

func (e *Engine) stopTextChangeTimer() {
	if e.textChangeTimer != nil {
		e.textChangeTimer.Stop()
		e.textChangeTimer = nil
	}
}
