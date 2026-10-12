package middleware

// These endpoints perform their own transactional reservation. Reads and stop
// must also remain available when the reservation uses the remaining balance.
func isDigitalTeacherRequest(method, route string) bool {
	switch method {
	case "GET":
		return route == "/v1/digital-teachers/settings" || route == "/v1/digital-teachers/catalog" || route == "/v1/digital-teachers/sessions" || route == "/v1/digital-teachers/sessions/:id"
	case "PUT":
		return route == "/v1/digital-teachers/teachers"
	case "POST":
		return route == "/v1/digital-teachers/sessions" || route == "/v1/digital-teachers/sessions/:id/stop"
	}
	return false
}
func isDigitalTeacherSDKPath(path string) bool {
	return path == "/v1/digital-teachers/sdk/v1/sessions/start" || path == "/v1/digital-teachers/sdk/v1/sessions/stop" || path == "/v1/digital-teachers/sdk/v1/sessions/keep-alive"
}
