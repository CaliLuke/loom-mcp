package runtime

// HasEventInterceptors reports whether runtime or registered agent middleware can
// alter observed events. RuntimeInterceptorFuncs with no BeforeEventFunc are
// excluded. The check is conservative across every registered agent, including
// agents that may run as children.
func (r *Runtime) HasEventInterceptors() bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if hasEventInterceptor(r.interceptors) {
		return true
	}
	for _, registration := range r.agents {
		if hasEventInterceptor(registration.mergedInterceptors) {
			return true
		}
	}
	return false
}

func hasEventInterceptor(interceptors []Interceptor) bool {
	for _, interceptor := range interceptors {
		switch value := interceptor.(type) {
		case RuntimeInterceptorFuncs:
			if value.BeforeEventFunc != nil {
				return true
			}
		case *RuntimeInterceptorFuncs:
			if value != nil && value.BeforeEventFunc != nil {
				return true
			}
		default:
			if _, ok := interceptor.(EventInterceptor); ok {
				return true
			}
		}
	}
	return false
}
