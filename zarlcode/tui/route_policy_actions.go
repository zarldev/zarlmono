package tui

// handleRoutePolicyAction keeps both editors' immediate UI effects together;
// asynchronous construction and admission remain owned by the repoint lifecycle.
func (m *UI) handleRoutePolicyAction(a action) bool {
	switch a := a.(type) {
	case actionEditRouteProtocol:
		m.editRouteProtocol()
	case actionRouteProtocolRead:
		m.annotateRouteProtocolEditor()
	case actionRouteProtocolSaved:
		m.routeProtocolSaved(a)
		m.annotateRouteProtocolEditor()
	case actionEditRouteCache:
		m.editRouteCache()
	case actionRouteCacheRead:
		m.annotateRouteCacheEditor()
	case actionRouteCacheSaved:
		m.routeCacheSaved(a)
		m.annotateRouteCacheEditor()
	default:
		return false
	}
	return true
}
