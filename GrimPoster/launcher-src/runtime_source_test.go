package main

import (
	"encoding/json"
	"errors"
	"testing"
)

type recordingRuntime struct {
	calls    []string
	fail     string
	response json.RawMessage
}

func (c *recordingRuntime) command(method string, params any) (json.RawMessage, error) {
	c.calls = append(c.calls, method)
	if method == c.fail {
		return nil, errors.New("connection lost")
	}
	if method == "Runtime.evaluate" {
		return c.response, nil
	}
	return json.RawMessage(`{"identifier":"1"}`), nil
}
func TestRuntimeLoadsCurrentDocumentAndFutureNavigations(t *testing.T) {
	c := &recordingRuntime{response: json.RawMessage(`{"result":{"type":"undefined"}}`)}
	if err := installRuntime(c); err != nil {
		t.Fatal(err)
	}
	if len(c.calls) != 2 || c.calls[0] != "Page.addScriptToEvaluateOnNewDocument" || c.calls[1] != "Runtime.evaluate" {
		t.Fatal(c.calls)
	}
}
func TestRuntimeFailureCannotReportReady(t *testing.T) {
	for _, c := range []*recordingRuntime{
		{fail: "Page.addScriptToEvaluateOnNewDocument"},
		{fail: "Runtime.evaluate"},
		{response: json.RawMessage(`{"exceptionDetails":{"text":"Script failed"}}`)},
		{response: json.RawMessage(`invalid`)},
	} {
		if err := installRuntime(c); err == nil {
			t.Fatal("runtime failure was accepted")
		}
	}
}
func TestRuntimeReadyRequiresMatchingGame(t *testing.T) {
	s := stateSnapshot{Installed: &installedBOTC{Version: "3.58.0", SHA256: "abc"}, Patch: patchManifest{AppVersion: "3.58.0", ExpectedPatchedSha256: "abc", PatchVerification: "runtime-injection"}}
	if !s.ready() || !s.canLaunch() {
		t.Fatal("verified runtime should launch offline")
	}
	s.Installed.SHA256 = "changed"
	if s.ready() || s.canLaunch() {
		t.Fatal("modified executable must not launch")
	}
}
