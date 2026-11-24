package web

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"os"
	"strings"
	"testing"

	"github.com/TomKalina/pi-velvetronix/cmd/fakeamp/sim"
	"github.com/TomKalina/pi-velvetronix/internal/bridge"
)

// simAmp plugs in the amp and answers the bridge with the simulator.
func simAmp(t *testing.T, h *harness) (*sim.Sim, *fakePort) {
	t.Helper()
	amp := sim.New()
	h.devices(true)
	p := <-h.ports
	go func() {
		for {
			select {
			case f := <-p.out:
				for _, r := range amp.Handle(f) {
					select {
					case p.in <- r:
					case <-p.closed:
						return
					}
				}
			case <-p.closed:
				return
			}
		}
	}()
	waitUntil(t, func() bool { return h.bridge.Status().State == bridge.StateConnected })
	return amp, p
}

func mcpToken(t *testing.T, h *harness) string {
	t.Helper()
	resp, body := h.do(t, loggedIn(t, h), "POST", "/api/mcp/token", "")
	if resp.StatusCode != 200 || body["token"] == "" {
		t.Fatalf("token: %d %v", resp.StatusCode, body)
	}
	return body["token"].(string)
}

// rpc posts one JSON-RPC message to /mcp.
func rpc(t *testing.T, h *harness, token, body string) (*http.Response, map[string]any) {
	t.Helper()
	req, _ := http.NewRequest("POST", h.ts.URL+"/mcp", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var m map[string]any
	json.NewDecoder(resp.Body).Decode(&m)
	return resp, m
}

// callTool calls a tool and returns its text and isError.
func callTool(t *testing.T, h *harness, token, name, args string) (string, bool) {
	t.Helper()
	resp, m := rpc(t, h, token, `{"jsonrpc":"2.0","id":7,"method":"tools/call","params":{"name":"`+name+`","arguments":`+args+`}}`)
	if resp.StatusCode != 200 || m["error"] != nil {
		t.Fatalf("%s: %d %v", name, resp.StatusCode, m)
	}
	res := m["result"].(map[string]any)
	isErr, _ := res["isError"].(bool)
	return res["content"].([]any)[0].(map[string]any)["text"].(string), isErr
}

func TestMCPAuth(t *testing.T) {
	h := newHarness(t, fakeAuthed(), nil)
	if resp, _ := h.do(t, http.DefaultClient, "POST", "/api/mcp/token", ""); resp.StatusCode != 401 {
		t.Fatalf("token without login: %d", resp.StatusCode)
	}
	init := `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18"}}`
	for _, tok := range []string{"", "nope"} {
		resp, _ := rpc(t, h, tok, init)
		if resp.StatusCode != 401 || !strings.HasPrefix(resp.Header.Get("WWW-Authenticate"), "Bearer") {
			t.Fatalf("token %q: %d %q", tok, resp.StatusCode, resp.Header.Get("WWW-Authenticate"))
		}
	}
	old := mcpToken(t, h)
	tok := mcpToken(t, h)
	if resp, _ := rpc(t, h, old, init); resp.StatusCode != 401 {
		t.Fatalf("replaced token: %d", resp.StatusCode)
	}
	if resp, _ := rpc(t, h, tok, init); resp.StatusCode != 200 {
		t.Fatalf("token: %d", resp.StatusCode)
	}
	// The token in the path, for clients without headers.
	for path, want := range map[string]int{"/mcp/" + tok: 200, "/mcp/" + old: 401, "/mcp/x": 401} {
		resp, err := http.Post(h.ts.URL+path, "application/json", strings.NewReader(init))
		if err != nil || resp.StatusCode != want {
			t.Fatalf("%s: %v %v, want %d", path, resp, err, want)
		}
	}
	// A browser page on another site cannot use the endpoint.
	req, _ := http.NewRequest("POST", h.ts.URL+"/mcp", strings.NewReader(init))
	req.Header.Set("Authorization", "Bearer "+tok)
	req.Header.Set("Origin", "https://evil.example")
	req.Header.Set("Sec-Fetch-Site", "cross-site")
	if resp, err := http.DefaultClient.Do(req); err != nil || resp.StatusCode != 403 {
		t.Fatalf("cross-site: %v %v", resp, err)
	}
}

func TestMCPProtocol(t *testing.T) {
	h := newHarness(t, fakeAuthed(), nil)
	tok := mcpToken(t, h)
	resp, m := rpc(t, h, tok, `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-03-26","capabilities":{},"clientInfo":{"name":"t","version":"1"}}}`)
	res := m["result"].(map[string]any)
	if resp.StatusCode != 200 || m["id"] != 1.0 || res["protocolVersion"] != "2025-03-26" || res["capabilities"].(map[string]any)["tools"] == nil {
		t.Fatalf("initialize: %d %v", resp.StatusCode, m)
	}
	if _, m := rpc(t, h, tok, `{"jsonrpc":"2.0","id":"a","method":"initialize","params":{"protocolVersion":"2099-01-01"}}`); m["result"].(map[string]any)["protocolVersion"] != "2025-06-18" {
		t.Fatalf("unknown version: %v", m)
	}
	if resp, _ := rpc(t, h, tok, `{"jsonrpc":"2.0","method":"notifications/initialized"}`); resp.StatusCode != 202 {
		t.Fatalf("notification: %d", resp.StatusCode)
	}
	_, m = rpc(t, h, tok, `{"jsonrpc":"2.0","id":2,"method":"tools/list"}`)
	list := m["result"].(map[string]any)["tools"].([]any)
	names := map[string]bool{}
	for _, x := range list {
		tl := x.(map[string]any)
		names[tl["name"].(string)] = true
		if tl["inputSchema"].(map[string]any)["type"] != "object" || tl["description"] == "" {
			t.Fatalf("tool %v", tl)
		}
	}
	for _, n := range []string{"vox_status", "vox_catalog", "vox_set_sound", "vox_list_songs", "vox_play_song",
		"vox_read_vtxprog", "vox_play_vtxprog", "vox_select_program", "vox_save_program", "vox_disconnect"} {
		if !names[n] {
			t.Errorf("tool %s missing", n)
		}
	}
	if _, m := rpc(t, h, tok, `{"jsonrpc":"2.0","id":3,"method":"nope"}`); m["error"].(map[string]any)["code"] != -32601.0 {
		t.Fatalf("unknown method: %v", m)
	}
	if resp, _ := rpc(t, h, tok, `{bad`); resp.StatusCode != 400 {
		t.Fatalf("bad JSON: %d", resp.StatusCode)
	}
	req, _ := http.NewRequest("GET", h.ts.URL+"/mcp", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	if resp, _ := http.DefaultClient.Do(req); resp.StatusCode != 405 {
		t.Fatalf("GET: %d", resp.StatusCode)
	}
	text, isErr := callTool(t, h, tok, "vox_catalog", `{}`)
	if isErr || !strings.Contains(text, `"BRIT 800"`) {
		t.Fatalf("catalog: %s", text)
	}
	text, _ = callTool(t, h, tok, "vox_list_songs", `{"query":"ramones"}`)
	if !strings.Contains(text, "ramones-blitzkrieg-bop") || strings.Contains(text, "sex-pistols") {
		t.Fatalf("songs: %s", text)
	}
	if text, isErr := callTool(t, h, tok, "vox_status", `{"bogus":1}`); !isErr || !strings.Contains(text, "Invalid arguments") {
		t.Fatalf("unknown argument: %s", text)
	}
}

func TestMCPAmpDisconnected(t *testing.T) {
	h := newHarness(t, fakeAuthed(), nil)
	tok := mcpToken(t, h)
	text, isErr := callTool(t, h, tok, "vox_status", `{}`)
	if !isErr || text != "Amp request failed: amp_disconnected" {
		t.Fatalf("status: %v %s", isErr, text)
	}
	if n := h.bridge.Controllers(); n != 0 {
		t.Fatalf("%d controllers after a failed call", n)
	}
}

func TestMCPControlsAmp(t *testing.T) {
	h := newHarness(t, fakeAuthed(), nil)
	tok := mcpToken(t, h)
	amp, port := simAmp(t, h)

	// A browser page that is open meanwhile sees what MCP does (kind 0x02 echoes).
	page, _, err := dialWS(t, h, "tok", nil)
	if err != nil {
		t.Fatal(err)
	}
	page.text(t, "hello")

	text, isErr := callTool(t, h, tok, "vox_status", `{}`)
	var st struct {
		Mode, Program string
		Current       struct{ Name string }
		Slots         map[string]string
	}
	if json.Unmarshal([]byte(text), &st); isErr || st.Mode != "user" || st.Program != "A1" || st.Current.Name != "FAKE A1" || st.Slots["B4"] != "FAKE B4" {
		t.Fatalf("status: %s", text)
	}

	text, isErr = callTool(t, h, tok, "vox_set_sound", `{"amp":{"model":"brit 800","gain":77,"bias":"HOT"},"pedal1":{"on":true,"type":"FUZZ","params":{"drive":81}}}`)
	if isErr {
		t.Fatalf("set_sound: %s", text)
	}
	var set struct {
		Result struct {
			Total, Done int
			Failed      []any
			Aborted     *string
		}
		Current struct {
			Amp    map[string]any
			Pedal1 struct {
				On     bool
				Type   string
				Params map[string]int
			}
		}
	}
	json.Unmarshal([]byte(text), &set)
	if set.Result.Done != set.Result.Total || len(set.Result.Failed) != 0 || set.Result.Aborted != nil ||
		set.Current.Amp["model"] != "BRIT 800" || set.Current.Amp["GAIN"] != 77.0 || set.Current.Amp["BIAS SHIFT"] != "HOT" ||
		!set.Current.Pedal1.On || set.Current.Pedal1.Type != "FUZZ" || set.Current.Pedal1.Params["DRIVE"] != 81 || set.Current.Pedal1.Params["TONE"] != 50 {
		t.Fatalf("set_sound: %s", text)
	}
	page.binary(t, append([]byte{kindEcho}, korg(0x41, 0x04, 0x00, 77, 0x00)...))

	if text, isErr := callTool(t, h, tok, "vox_set_sound", `{"amp":{"gain":101}}`); !isErr || !strings.Contains(text, "Invalid value") {
		t.Fatalf("gain 101: %s", text)
	}
	if text, isErr := callTool(t, h, tok, "vox_set_sound", `{"pedal2":{"params":{"FOO":1}}}`); !isErr || !strings.Contains(text, `no param "foo"`) {
		t.Fatalf("unknown param: %s", text)
	}

	// GAIN turned on the amp's panel between two calls.
	port.in <- amp.KnobTurn()
	page.binary(t, append([]byte{0x00}, korg(0x41, 0x04, 0x00, (77+7)%101, 0x00)...))
	text, _ = callTool(t, h, tok, "vox_status", `{}`)
	if !strings.Contains(text, `"GAIN": 84`) {
		t.Fatalf("status after the knob: %s", text)
	}

	text, isErr = callTool(t, h, tok, "vox_play_song", `{"id":"ramones-blitzkrieg-bop"}`)
	if isErr || !strings.Contains(text, `"done": `) || strings.Contains(text, `"aborted": "`) {
		t.Fatalf("play_song: %s", text)
	}

	text, isErr = callTool(t, h, tok, "vox_select_program", `{"slot":"b2"}`)
	if json.Unmarshal([]byte(text), &st); isErr || st.Program != "B2" || st.Current.Name != "FAKE B2" {
		t.Fatalf("select: %s", text)
	}
	text, isErr = callTool(t, h, tok, "vox_save_program", `{"slot":"A3","name":"MCP SAVE"}`)
	var save struct{ Slot, Name, Overwritten, Check string }
	if json.Unmarshal([]byte(text), &save); isErr || save.Slot != "A3" || save.Overwritten != "FAKE A3" || save.Check != "verified" {
		t.Fatalf("save: %s", text)
	}
	text, _ = callTool(t, h, tok, "vox_status", `{}`)
	if json.Unmarshal([]byte(text), &st); st.Slots["A3"] != "MCP SAVE" || st.Program != "A3" {
		t.Fatalf("status after save: %s", text)
	}
	if !strings.Contains(h.logs.String(), "save requested: func=4C slot=2") {
		t.Fatal("save not logged")
	}

	if n := h.bridge.Controllers(); n != 2 {
		t.Fatalf("%d controllers, want page + MCP", n)
	}
	if text, isErr := callTool(t, h, tok, "vox_disconnect", `{}`); isErr {
		t.Fatalf("disconnect: %s", text)
	}
	if n := h.bridge.Controllers(); n != 1 {
		t.Fatalf("%d controllers after disconnect", n)
	}
}

func TestMCPVtxprog(t *testing.T) {
	h := newHarness(t, fakeAuthed(), nil)
	tok := mcpToken(t, h)
	simAmp(t, h)
	file, err := os.ReadFile("../../web/research/vtxprog/marcosmoura/Ghost.vtxprog")
	if err != nil {
		t.Fatal(err)
	}
	data := base64.StdEncoding.EncodeToString(file)
	text, isErr := callTool(t, h, tok, "vox_read_vtxprog", `{"data":"`+data+`"}`)
	var progs []struct {
		Index int
		Name  string
	}
	if json.Unmarshal([]byte(text), &progs); isErr || len(progs) == 0 || progs[0].Name == "" {
		t.Fatalf("read: %s", text)
	}
	text, isErr = callTool(t, h, tok, "vox_play_vtxprog", `{"data":"`+data+`","index":0}`)
	if isErr || !strings.Contains(text, `"program": "`+progs[0].Name+`"`) || strings.Contains(text, `"aborted": "`) {
		t.Fatalf("play: %s", text)
	}
	if text, isErr := callTool(t, h, tok, "vox_read_vtxprog", `{"data":"bm9wZQ=="}`); !isErr || !strings.Contains(text, "not_vtxprog") {
		t.Fatalf("bad file: %s", text)
	}
}
