package passocontract

// Board REST is maintained from the canonical mounted handlers and
// docs/console/BOARD-REST-CONTRACT.md. It is separate from OpenAPI-generated
// snapshots because the backend does not yet publish this surface as OpenAPI.
const BoardRESTSource = "PyxCloud/pyx-backend:go/internal/resource/vibe/routes.go + board_console_routes.go + board_execution_routes.go"

func init() {
	actions := map[string]struct{ method, path string }{
		"status": {"GET", "/status"}, "list": {"GET", "/features"}, "task": {"GET", "/tasks/{taskId}"}, "claim": {"POST", "/tasks/{taskId}/claim"}, "release": {"POST", "/tasks/{taskId}/release"}, "resume": {"POST", "/tasks/{taskId}/resume"}, "plan": {"PUT", "/tasks/{taskId}/plan"}, "execute": {"POST", "/tasks/{taskId}/execute"}, "latest": {"GET", "/tasks/{taskId}/execution"}, "availability": {"GET", "/tasks/{taskId}/execution-availability"}, "verify": {"POST", "/tasks/{taskId}/verify"}, "complete": {"POST", "/tasks/{taskId}/complete"}, "execution": {"GET", "/executions/{executionId}"}, "evidence": {"GET", "/artifacts/{artifactId}"}}
	for name, a := range actions {
		key := "board-rest:" + name
		Operations[key] = Operation{Contract: "board-rest", ID: name, Method: a.method, Path: "/vibe/projects/{projectId}/board" + a.path}
		params := []string{"projectId"}
		switch name {
		case "status", "list":
		case "execution":
			params = append(params, "executionId")
		case "evidence":
			params = append(params, "artifactId")
		default:
			params = append(params, "taskId")
		}
		operationPathParams[key] = params
	}
}
