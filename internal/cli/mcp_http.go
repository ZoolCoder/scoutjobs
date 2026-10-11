package cli

import (
	"encoding/json"
	"io"
	"net/http"
	"sync"
)

// mcpHTTP serves the same tools as `scoutjobs mcp` over the MCP Streamable HTTP transport, for clients
// that cannot spawn a local process: hosted n8n's MCP client, claude.ai custom connectors, cloud agents.
// Every request is answered with one JSON body; the server offers no SSE stream. The dashboard mounts it
// on /mcp behind api_token, so authentication and the origin check live there.
func mcpHTTP() http.Handler {
	var mu sync.Mutex // tools are CLI commands; one at a time keeps them as ordered as on stdio
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.Header().Set("Allow", "POST")
			http.Error(w, "POST JSON-RPC messages here; this server offers no event stream", http.StatusMethodNotAllowed)
			return
		}
		body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 16<<20))
		if err != nil {
			http.Error(w, err.Error(), http.StatusRequestEntityTooLarge)
			return
		}
		var req rpcRequest
		if err := json.Unmarshal(body, &req); err != nil {
			writeRPC(w, http.StatusBadRequest, rpcResponse{JSONRPC: "2.0", ID: json.RawMessage("null"), Error: &rpcError{-32700, "parse error: send one JSON-RPC message per request"}})
			return
		}
		mu.Lock()
		resp, reply := mcpHandle(req)
		mu.Unlock()
		if !reply {
			w.WriteHeader(http.StatusAccepted) // notification or client response
			return
		}
		writeRPC(w, http.StatusOK, resp)
	})
}

func writeRPC(w http.ResponseWriter, code int, resp rpcResponse) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(resp)
}
