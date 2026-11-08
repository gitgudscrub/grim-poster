package main

import (
	_ "embed"
	"encoding/json"
	"errors"
)

//go:embed runtime.js
var runtimeSource string

type runtimeCommander interface {
	command(string, any) (json.RawMessage, error)
}

func installRuntime(c runtimeCommander) error {
	if _, err := c.command("Page.addScriptToEvaluateOnNewDocument", map[string]any{"source": runtimeSource}); err != nil {
		return err
	}
	result, err := c.command("Runtime.evaluate", map[string]any{"expression": runtimeSource, "returnByValue": true})
	if err != nil {
		return err
	}
	var evaluation struct{ ExceptionDetails json.RawMessage }
	if err = json.Unmarshal(result, &evaluation); err != nil {
		return err
	}
	if len(evaluation.ExceptionDetails) > 0 {
		return errors.New("Grim Poster could not initialize in the BOTC window")
	}
	return nil
}
